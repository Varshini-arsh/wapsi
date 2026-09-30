/**
 * WAPSI REST gateway.
 *
 * Modes:
 *   WAPSI_MODE=demo    (default) in-memory ledger — used for the live demo
 *   WAPSI_MODE=fabric  submit/query through the Drunix/Fabric network via
 *                      @hyperledger/fabric-gateway (see fabric.js)
 *
 * The caller's org identity is simulated with the X-WAPSI-MSP header in demo
 * mode (the UI "logged in as" switcher). In fabric mode it comes from the
 * loaded gateway identity.
 */
"use strict";

const express = require("express");
const cors = require("cors");

const demo = require("./demo");
const { seedScenario } = require("./seed");
// fabric.js is lazy-required only in fabric mode (it needs optional deps).

const app = express();
app.use(cors());
app.use(express.json({ limit: "1mb" }));

const MODE = process.env.WAPSI_MODE || "demo";
const PORT = process.env.PORT || 4000;

// Middleware: caller org (demo mode only). In fabric mode the identity is fixed.
app.use((req, _res, next) => {
  req.msp = req.get("X-WAPSI-MSP") || "Org1MSP";
  next();
});

function wrap(fn) {
  return async (req, res) => {
    try {
      const out = MODE === "fabric"
        ? await require("./fabric").submitOrEval(req.msp, fn.name, req)
        : fn(req.msp, req);
      res.json(out);
    } catch (e) {
      res.status(e.status || 500).json({ error: e.message });
    }
  };
}

// ---- operations (demo implementations; fabric mode routes by fn name) ----
// NOTE: every handler passed to wrap() MUST be a named function — fabric mode
// dispatches on fn.name. Arrow functions assigned to a const keep that name.
const ops = {
  CreateCase: (msp, req) => demo.CreateCase(msp, req.body || {}),
  AttestHop: (msp, req) => demo.AttestHop(msp, req.params.caseId, req.body.hop || req.body),
  ComputeTrace: (msp, req) => demo.ComputeTrace(msp, req.params.caseId, (req.body || {}).rule || req.query.rule || "LIBR"),
  RecordLien: (msp, req) => demo.RecordLien(msp, req.params.caseId, req.body || {}),
  FlagDeadline: (msp, req) => demo.FlagDeadline(msp, req.params.caseId, (req.body || {}).lienId, (req.body || {}).asOf),
  ProposeRestoration: (msp, req) => demo.ProposeRestoration(msp, req.params.caseId, (req.body || {}).rule),
  ApproveAction: (msp, req) => demo.ApproveAction(msp, req.params.caseId, (req.body || {}).actionId),
};
// Route-name aliases so fabric mode can dispatch by fn name (fn.name === key).
const AttestHopBody = ops.AttestHop;
const ComputeTraceBody = ops.ComputeTrace;
const RecordLienBody = ops.RecordLien;
const FlagDeadlineBody = ops.FlagDeadline;
const ProposeRestorationBody = ops.ProposeRestoration;
const ApproveActionBody = ops.ApproveAction;

// ---- fabric-mode reads through the chaincode (read-only evaluates) ----
async function fabricRead(res, req, fnName, ...args) {
  try {
    const out = await require("./fabric").evaluate(req.msp, fnName, args);
    res.json(out);
  } catch (e) {
    res.status(e.status || 500).json({ error: e.message });
  }
}

// ---- REST routes ----
app.post("/api/cases", wrap(ops.CreateCase));
app.get("/api/cases/:caseId", (req, res) => {
  if (MODE === "fabric") return fabricRead(res, req, "GetCase", req.params.caseId);
  const fc = demo.state.cases.get(req.params.caseId);
  if (!fc) return res.status(404).json({ error: "case not found" });
  res.json(fc);
});
app.post("/api/cases/:caseId/hops", wrap(AttestHopBody));
app.post("/api/cases/:caseId/trace", wrap(ComputeTraceBody));
app.get("/api/cases/:caseId/trace", (req, res) => {
  if (MODE === "fabric") return fabricRead(res, req, "GetTrace", req.params.caseId);
  const t = demo.state.traces.get(req.params.caseId);
  if (!t) return res.status(404).json({ error: "no trace yet; compute one first" });
  res.json(t);
});
app.post("/api/cases/:caseId/liens", wrap(RecordLienBody));
app.get("/api/cases/:caseId/liens", (req, res) => {
  if (MODE === "fabric") return fabricRead(res, req, "GetLiens", req.params.caseId);
  res.json(demo.state.liens.get(req.params.caseId) || []);
});
app.post("/api/cases/:caseId/hops/:hopId/flag-deadline", wrap(FlagDeadlineBody));
app.post("/api/cases/:caseId/proposals", wrap(ProposeRestorationBody));
app.post("/api/cases/:caseId/approvals", wrap(ApproveActionBody));
app.get("/api/cases/:caseId/hops", (req, res) => {
  if (MODE === "fabric") {
    // No per-hop read function exists in the chaincode (GetCaseHops is private);
    // hops are visible inside the trace's notes/accounts instead.
    res.set("X-WAPSI-Note", "hops not readable from ledger; see trace accounts/notes");
    return res.json([]);
  }
  res.json(demo.state.hops.get(req.params.caseId) || []);
});

// One-shot demo seed: creates case C001, attests all hops (with correct
// per-hop caller orgs), computes the initial trace.
app.post("/api/demo/seed", (_req, res) => {
  try {
    res.json(seedScenario(demo));
  } catch (e) {
    res.status(e.status || 500).json({ error: e.message });
  }
});

app.get("/api/health", (_req, res) => res.json({ mode: MODE, ok: true }));

app.listen(PORT, () => {
  console.log(`WAPSI gateway (${MODE} mode) listening on http://localhost:${PORT}`);
});
