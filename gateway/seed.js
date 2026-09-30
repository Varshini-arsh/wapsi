/**
 * WAPSI demo seed: creates case C001 and attests every hop with the correct
 * per-hop caller org, mirroring seed.json from the simulator.
 */
"use strict";

function seedScenario(demo) {
  const { CreateCase, AttestHop, ComputeTrace } = demo;
  const P = 100;

  const hopRows = [
    // hopId, ts, srcBank, srcAcctId, dstBank, dstAcctId, amount(p), own Funding?, cashOutDst?, note
    ["H0", "2026-09-29T07:30:00Z", "Org3MSP", "RAVI", "Org3MSP", "RAVI", 80000, true, false, "Ravi's opening balance (legitimate)"],
    ["H1", "2026-09-29T08:00:00Z", "Org1MSP", "PRIYA", "Org2MSP", "A", 40000 * P, false, false, "Priya defrauded of Rs 40,000"],
    ["H2", "2026-09-29T08:01:00Z", "Org1MSP", "ARJUN", "Org2MSP", "A", 25000 * P, false, false, "Arjun defrauded of Rs 25,000"],
    ["H3", "2026-09-29T08:02:00Z", "Org1MSP", "MEENA", "Org3MSP", "B", 15000 * P, false, false, "Meena defrauded of Rs 15,000"],
    ["H4", "2026-09-29T08:05:00Z", "Org2MSP", "A", "Org3MSP", "B", 50000 * P, false, false, "Layering: mule A sweeps Rs 50,000 to mule B"],
    ["H5", "2026-09-29T08:12:00Z", "Org3MSP", "B", "Org3MSP", "B", 35000 * P, true, false, "Mule B's own Rs 35,000 salary arrives (mixing)"],
    ["H6", "2026-09-29T08:20:00Z", "Org3MSP", "B", "Org3MSP", "RAVI", 10000 * P, false, false, "Mule B pays Ravi Rs 10,000 (innocent purchase)"],
    ["H7", "2026-09-29T08:25:00Z", "Org3MSP", "B", "Org3MSP", "W1", 45000 * P, false, true, "Mule cash-out of Rs 45,000"],
  ];

  // Simple demo HMAC-like tokens (stable across restarts).
  const tokens = {
    PRIYA: "e0c8006b6a6bbf7d", ARJUN: "056d7b5fa4a38ac4", MEENA: "4d77e667f089d209",
    A: "a11ce0de1a7b5c00", B: "b22df9ce2b8c4d11", RAVI: "c33ae0bf3c9d5e22",
  };
  const tok = (bank, id) => (id === "W1" ? (tokens.W1 || "d44bf9c04da6f133") : (tokens[id] || id.toLowerCase()));

  const fc = CreateCase("Org1MSP", {
    caseId: "C001",
    complaintRef: "NCRP-2026-0931117",
    victimBank: "Org1MSP",
    claimedAmount: 80000 * P,
    reportedAt: "2026-09-29T09:05:00Z",
  });

  for (const [hopId, timestamp, srcBank, srcId, dstBank, dstId, amount, ownFund, cashOutDst] of hopRows) {
    AttestHop(ownFund ? srcBank : srcBank, "C001", {
      hopId,
      srcBank,
      srcAcctToken: ownFund ? "OWNFUND:" + tok(srcBank, srcId) : tok(srcBank, srcId),
      dstBank,
      dstAcctToken: cashOutDst ? "CASH_OUT:" + tok(dstBank, dstId) : tok(dstBank, dstId),
      amount,
      timestamp,
      paymentRef: `UPI/${hopId}`,
    });
  }

  const trace = ComputeTrace("Org4MSP", "C001", "LIBR");
  return { case: fc, hops: hopRows.length, trace };
}

module.exports = { seedScenario };
