/**
 * WAPSI gateway — in-memory demo mode.
 *
 * Mirrors the chaincode's semantics (access control by org, lien caps,
 * approval majority) using the same Python/Go-validated tracing engine
 * semantics via the embedded JS engine (engine.js). This is what runs
 * during the live demo so the network is never a single point of failure.
 */
"use strict";

const engine = require("./engine");

// Ledger state
const state = {
  cases: new Map(),     // caseId -> FraudCase
  hops: new Map(),      // caseId -> [TransactionHop]
  liens: new Map(),     // caseId -> [Lien]
  proposals: new Map(), // actionId -> ActionProposal
  traces: new Map(),    // caseId -> TraceResult
};

const LEA_MSP = "Org4MSP";
const LIEN_DAYS = 90;

function nowISO() {
  return new Date().toISOString();
}

function err(status, msg) {
  const e = new Error(msg);
  e.status = status;
  return e;
}

// ---------------------------------------------------------------------------
// Chaincode-equivalent functions. `msp` simulates the caller's org identity.
// ---------------------------------------------------------------------------
function CreateCase(msp, { caseId, complaintRef, victimBank, claimedAmount, reportedAt }) {
  if (!caseId || !complaintRef || !victimBank) throw err(400, "caseId, complaintRef and victimBank are required");
  if (!(claimedAmount > 0)) throw err(400, "claimedAmount must be positive");
  if (state.cases.has(caseId)) throw err(409, `case ${caseId} already exists`);
  const fc = {
    caseId, complaintRef, victimBank, claimedAmount,
    reportedAt: reportedAt || nowISO(),
    status: "OPEN", createdBy: msp,
  };
  state.cases.set(caseId, fc);
  state.hops.set(caseId, []);
  state.liens.set(caseId, []);
  return fc;
}

function AttestHop(msp, caseId, h) {
  const hops = state.hops.get(caseId);
  if (!hops) throw err(404, `case ${caseId} not found`);
  const { hopId, srcBank, srcAcctToken, dstBank, dstAcctToken, amount, timestamp, paymentRef } = h;
  if (!hopId || !srcBank || !srcAcctToken || !dstBank || !dstAcctToken) throw err(400, "hop fields missing");
  if (!(amount > 0)) throw err(400, "amount must be positive");
  if (msp !== srcBank) throw err(403, `only the source bank (${srcBank}) may attest this hop; caller is ${msp}`);
  if (hops.some((x) => x.hopId === hopId)) throw err(409, `hop ${hopId} already attested`);
  const hop = {
    caseId, hopId, srcBank, srcAcctToken, dstBank, dstAcctToken,
    amount, timestamp, paymentRef: paymentRef || "", attestedBy: msp, attestedAt: nowISO(),
  };
  hops.push(hop);
  return hop;
}

function ComputeTrace(msp, caseId, rule) {
  const hops = state.hops.get(caseId);
  if (!hops) throw err(404, `case ${caseId} not found`);
  const res = engine.computeTrace(hops, rule);
  const fc = state.cases.get(caseId);
  res.totalClaimed = fc ? fc.claimedAmount : 0;
  res.caseId = caseId;
  res.computedAt = nowISO();
  state.traces.set(caseId, res);
  if (fc) fc.status = "TRACED";
  return res;
}

function RecordLien(msp, caseId, { bank, acctToken, lienAmount }) {
  const trace = state.traces.get(caseId);
  if (!trace) throw err(409, "no trace stored for case; run ComputeTrace first");
  const acct = (trace.accounts || []).find((a) => a.acctToken === acctToken);
  if (!acct) throw err(404, `account not part of the traced trail for case ${caseId}`);
  if (lienAmount > acct.lienable) {
    throw err(422, `lien ${lienAmount} exceeds traced recoverable ${acct.lienable} for ${acctToken}; hold only the traced amount (MHA SOP)`);
  }
  if (msp !== bank && msp !== LEA_MSP) throw err(403, `lien must be recorded by the holding bank or the LEA`);
  const now = nowISO();
  const lien = {
    caseId,
    lienId: `L${caseId}-${acctToken.slice(0, 8)}`,
    bank, acctToken, lienAmount,
    recordedAt: now,
    deadline: new Date(Date.now() + LIEN_DAYS * 864e5).toISOString(),
    status: "ACTIVE", recordedBy: msp,
  };
  state.liens.get(caseId).push(lien);
  const fc = state.cases.get(caseId);
  if (fc) fc.status = "LIENS_RECORDED";
  return lien;
}

function FlagDeadline(msp, caseId, lienId, asOf) {
  const lien = state.liens.get(caseId)?.find((l) => l.lienId === lienId);
  if (!lien) throw err(404, `lien ${lienId} not found`);
  if (lien.status === "EXTENDED") return lien;
  const checkAt = asOf ? new Date(asOf) : new Date();
  if (checkAt >= new Date(lien.deadline)) lien.status = "RELEASE_ELIGIBLE";
  return lien;
}

function ProposeRestoration(msp, caseId, rule) {
  const trace = state.traces.get(caseId);
  if (!trace) throw err(409, "no trace stored; run ComputeTrace first");
  let res = trace;
  if (rule && rule !== trace.rule) res = ComputeTrace(msp, caseId, rule);
  const orgSet = new Set([LEA_MSP]);
  for (const h of state.hops.get(caseId) || []) {
    orgSet.add(h.srcBank);
    orgSet.add(h.dstBank);
  }
  const requiredOrgs = [...orgSet];
  const now = nowISO();
  const actionId = `A${caseId}-${Date.now()}`;
  const prop = {
    caseId, actionId, actionType: "RESTORATION", rule: res.rule,
    payload: JSON.stringify({ totalRecoverable: res.totalRecoverable, victims: res.victims }),
    requiredOrgs, approvals: [], status: "PENDING", createdAt: now,
  };
  state.proposals.set(actionId, prop);
  return prop;
}

function ApproveAction(msp, caseId, actionId) {
  const p = state.proposals.get(actionId);
  if (!p) throw err(404, `proposal ${actionId} not found`);
  if (p.status === "APPROVED") return p;
  if (!p.requiredOrgs.includes(msp)) throw err(403, `org ${msp} is not a required approver`);
  if (!p.approvals.includes(msp)) p.approvals.push(msp);
  const majority = Math.floor(p.requiredOrgs.length / 2) + 1;
  if (p.approvals.length >= majority) {
    p.status = "APPROVED";
    const fc = state.cases.get(caseId);
    if (fc) fc.status = "RESTORATION_APPROVED";
  }
  return p;
}

function assertCase(msp, caseId) {
  if (!state.cases.has(caseId)) throw err(404, `case ${caseId} not found`);
}

module.exports = {
  state, LEA_MSP,
  CreateCase, AttestHop, ComputeTrace, RecordLien, FlagDeadline, ProposeRestoration, ApproveAction,
  assertCase,
};
