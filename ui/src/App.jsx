import React, { useEffect, useMemo, useState } from "react";

// ---------------------------------------------------------------------------
// Demo constants (must match gateway/seed.js tokens)
// ---------------------------------------------------------------------------
const TOKENS = {
  PRIYA: "e0c8006b6a6bbf7d",
  ARJUN: "056d7b5fa4a38ac4",
  MEENA: "4d77e667f089d209",
  A: "a11ce0de1a7b5c00",
  B: "b22df9ce2b8c4d11",
  RAVI: "c33ae0bf3c9d5e22",
  W1: "CASH_OUT:d44bf9c04da6f133",
};

const DEMO_HOPS = [
  { hopId: "H0", ts: "07:30", srcBank: "Org3MSP", dstBank: "Org3MSP", src: "OWNFUND", dst: "RAVI", amount: 80000, own: true, label: "Ravi's opening balance (own funds)" },
  { hopId: "H1", ts: "08:00", srcBank: "Org1MSP", dstBank: "Org2MSP", src: "PRIYA", dst: "A", amount: 4000000, label: "Priya defrauded" },
  { hopId: "H2", ts: "08:01", srcBank: "Org1MSP", dstBank: "Org2MSP", src: "ARJUN", dst: "A", amount: 2500000, label: "Arjun defrauded" },
  { hopId: "H3", ts: "08:02", srcBank: "Org1MSP", dstBank: "Org3MSP", src: "MEENA", dst: "B", amount: 1500000, label: "Meena defrauded" },
  { hopId: "H4", ts: "08:05", srcBank: "Org2MSP", dstBank: "Org3MSP", src: "A", dst: "B", amount: 5000000, label: "Layering A → B" },
  { hopId: "H5", ts: "08:12", srcBank: "Org3MSP", dstBank: "Org3MSP", src: "OWNFUND", dst: "B", amount: 3500000, own: true, label: "Mule B's own salary (mixing)" },
  { hopId: "H6", ts: "08:20", srcBank: "Org3MSP", dstBank: "Org3MSP", src: "B", dst: "RAVI", amount: 1000000, label: "Mule B pays Ravi (innocent)" },
  { hopId: "H7", ts: "08:25", srcBank: "Org3MSP", dstBank: "Org3MSP", src: "B", dst: "W1", amount: 4500000, cashOut: true, label: "Mule cash-out" },
];

const tokenOf = (name) =>
  name === "OWNFUND" ? "OWNFUND:x" : name === "W1" ? TOKENS.W1 : TOKENS[name];

const NODES = [
  { id: "PRIYA", x: 90, y: 80, name: "Priya", sub: "Victim · SBI", kind: "victim" },
  { id: "ARJUN", x: 90, y: 210, name: "Arjun", sub: "Victim · SBI", kind: "victim" },
  { id: "MEENA", x: 90, y: 340, name: "Meena", sub: "Victim · SBI", kind: "victim" },
  { id: "A", x: 350, y: 145, name: "Mule A", sub: "HDFC", kind: "mule" },
  { id: "B", x: 590, y: 240, name: "Mule B", sub: "Axis · mixed funds", kind: "mule" },
  { id: "RAVI", x: 830, y: 130, name: "Ravi", sub: "Innocent shopkeeper · Axis", kind: "innocent" },
  { id: "W1", x: 830, y: 350, name: "Wallet W1", sub: "Cash-out sink", kind: "cashout" },
];

const ORGS = [
  { msp: "Org1MSP", label: "SBI (victim's bank)" },
  { msp: "Org2MSP", label: "HDFC" },
  { msp: "Org3MSP", label: "Axis" },
  { msp: "Org4MSP", label: "I4C / Police (LEA)" },
];

const RULES = [
  { id: "LIBR", desc: "Lowest Intermediate Balance Rule" },
  { id: "OWN_FIRST", desc: "Wrongdoer spends own money first" },
  { id: "FIFO", desc: "First in, first out" },
  { id: "PRORATA", desc: "Proportional sharing" },
];

const rs = (p) =>
  "₹" + (p / 100).toLocaleString("en-IN", { maximumFractionDigits: 2 });

async function api(path, msp, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: { "Content-Type": "application/json", "X-WAPSI-MSP": msp, ...(opts.headers || {}) },
  });
  const j = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(j.error || res.statusText);
  return j;
}

// ---------------------------------------------------------------------------
export default function App() {
  const [msp, setMsp] = useState("Org4MSP");
  const [kase, setKase] = useState(null);
  const [attested, setAttested] = useState([]); // hopIds attested
  const [trace, setTrace] = useState(null);
  const [rule, setRule] = useState("LIBR");
  const [liens, setLiens] = useState([]);
  const [proposal, setProposal] = useState(null);
  const [toast, setToast] = useState(null);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState("case");

  const flash = (msg, ok = true) => {
    setToast({ msg, ok });
    setTimeout(() => setToast(null), 3500);
  };
  const run = async (fn, okMsg) => {
    setBusy(true);
    try {
      const out = await fn();
      if (okMsg) flash(okMsg);
      return out;
    } catch (e) {
      flash(e.message, false);
      return null;
    } finally {
      setBusy(false);
    }
  };

  const refreshCase = () =>
    api("/api/cases/C001", msp).then(setKase).catch(() => setKase(null));

  useEffect(() => { refreshCase(); }, []);

  const pendingHops = DEMO_HOPS.filter((h) => !attested.includes(h.hopId));

  const initDemo = () => run(async () => {
    await api("/api/cases", "Org1MSP", {
      method: "POST",
      body: JSON.stringify({
        caseId: "C001", complaintRef: "NCRP-2026-0931117",
        victimBank: "Org1MSP", claimedAmount: 8000000,
        reportedAt: "2026-09-29T09:05:00Z",
      }),
    });
    setAttested([]);
    setTrace(null); setLiens([]); setProposal(null);
    await refreshCase();
  }, "Case C001 created on the ledger");

  const attest = (h) => run(async () => {
    await api(`/api/cases/C001/hops`, h.srcBank, {
      method: "POST",
      body: JSON.stringify({
        caseId: "C001", hop: {
          hopId: h.hopId,
          srcBank: h.srcBank,
          srcAcctToken: tokenOf(h.src),
          dstBank: h.dstBank,
          dstAcctToken: tokenOf(h.dst),
          amount: h.amount, timestamp: `2026-09-29T${h.ts}:00Z`, paymentRef: `UPI/${h.hopId}`,
        },
      }),
    });
    setAttested((a) => [...a, h.hopId]);
  }, `${h.hopId} attested by ${h.srcBank} — edge verified`);

  const attestAll = () => run(async () => {
    for (const h of pendingHops) {
      // eslint-disable-next-line no-await-in-loop
      await api(`/api/cases/C001/hops`, h.srcBank, {
        method: "POST",
        body: JSON.stringify({
          caseId: "C001", hop: {
            hopId: h.hopId,
            srcBank: h.srcBank,
            srcAcctToken: tokenOf(h.src),
            dstBank: h.dstBank,
            dstAcctToken: tokenOf(h.dst),
            amount: h.amount, timestamp: `2026-09-29T${h.ts}:00Z`, paymentRef: `UPI/${h.hopId}`,
          },
        }),
      });
    }
    setAttested(DEMO_HOPS.map((h) => h.hopId));
  }, "All hops attested — money trail fully verified");

  const compute = (r) => run(async () => {
    const t = await api(`/api/cases/C001/trace`, "Org4MSP", {
      method: "POST",
      body: JSON.stringify({ caseId: "C001", rule: r }),
    });
    setTrace(t); setRule(r);
  });

  const placeLien = (acct) => run(async () => {
    const l = await api(`/api/cases/C001/liens`, msp, {
      method: "POST",
      body: JSON.stringify({ caseId: "C001", bank: acct.bank, acctToken: acct.acctToken, lienAmount: acct.lienable }),
    });
    setLiens((ls) => [...ls.filter((x) => x.lienId !== l.lienId), l]);
  }, `Lien of ${rs(acct.lienable)} recorded — only the traced amount`);

  const flagDeadline = (lien) => run(async () => {
    const asOf = new Date(Date.now() + 91 * 864e5).toISOString();
    const l = await api(`/api/cases/C001/hops/x/flag-deadline`, msp, {
      method: "POST", body: JSON.stringify({ lienId: lien.lienId, asOf }),
    });
    setLiens((ls) => ls.map((x) => (x.lienId === l.lienId ? l : x)));
  }, "Deadline flagged: 90 days elapsed — RELEASE_ELIGIBLE (no auto-release)");

  const propose = () => run(async () => {
    const p = await api(`/api/cases/C001/proposals`, "Org4MSP", {
      method: "POST", body: JSON.stringify({ caseId: "C001", rule }),
    });
    setProposal(p);
  }, "Restoration proposal created from the trace");

  const approve = (org) => run(async () => {
    const p = await api(`/api/cases/C001/approvals`, org, {
      method: "POST", body: JSON.stringify({ caseId: "C001", actionId: proposal.actionId }),
    });
    setProposal(p);
    if (p.status === "APPROVED") flash("Restoration APPROVED — banks can now execute refunds");
  });

  // Wrongful-freeze-avoided metric: naive systems freeze the whole balance of
  // every downstream account; WAPSI freezes only the traced amount.
  const freezeAvoided = useMemo(() => {
    if (!trace) return 0;
    return trace.accounts
      .filter((a) => !a.acctToken.startsWith("CASH_OUT"))
      .reduce((s, a) => s + Math.max(0, a.remaining - a.lienable), 0);
  }, [trace]);

  const attestedSet = new Set(attested);
  const nodeLien = (id) => {
    if (!trace) return null;
    const a = trace.accounts.find((x) => x.acctToken === tokenOf(id));
    return a && a.lienable > 0 ? a.lienable : null;
  };

  return (
    <div className="app">
      <header>
        <div className="brand">
          <span className="logo">◈</span>
          <div>
            <h1>WAPSI</h1>
            <p>Verifiable fund provenance &amp; restoration · Drunix-compatible network</p>
          </div>
        </div>
        <div className="controls">
          <label>Acting as</label>
          <select value={msp} onChange={(e) => setMsp(e.target.value)}>
            {ORGS.map((o) => <option key={o.msp} value={o.msp}>{o.label}</option>)}
          </select>
          {!kase && <button className="primary" disabled={busy} onClick={initDemo}>① Create case C001</button>}
          {kase && <span className="badge">Case {kase.caseId} · {kase.status} · ref {kase.complaintRef}</span>}
        </div>
      </header>

      <nav>
        <button className={tab === "case" ? "on" : ""} onClick={() => setTab("case")}>Money trail</button>
        <button className={tab === "ops" ? "on" : ""} onClick={() => setTab("ops")}>
          Bank ops {pendingHops.length > 0 && <span className="pill">{pendingHops.length}</span>}
        </button>
      </nav>

      {tab === "case" && (
        <main className="case-tab">
          <section className="graph-card">
            <div className="legend">
              <span><i className="edge verified" /> verified hop</span>
              <span><i className="edge pending" /> not yet attested</span>
              <span><i className="edge own" /> legitimate funds</span>
            </div>
            <svg viewBox="0 0 1000 460" className="trail">
              {DEMO_HOPS.filter((h) => !h.own).map((h) => {
                const s = NODES.find((n) => n.id === h.src);
                const d = NODES.find((n) => n.id === h.dst);
                if (!s || !d) return null;
                const ok = attestedSet.has(h.hopId);
                const mx = (s.x + d.x) / 2, my = (s.y + d.y) / 2 - 14;
                return (
                  <g key={h.hopId}>
                    <line x1={s.x} y1={s.y} x2={d.x} y2={d.y}
                      className={`edge ${ok ? "verified" : "pending"} ${h.cashOut ? "cash" : ""}`} />
                    <text x={mx} y={my} className={`amt ${ok ? "verified" : ""}`}>
                      {h.hopId} · {rs(h.amount)}
                    </text>
                  </g>
                );
              })}
              {DEMO_HOPS.filter((h) => h.own).map((h) => {
                const d = NODES.find((n) => n.id === h.dst);
                if (!d) return null;
                const ok = attestedSet.has(h.hopId);
                return (
                  <g key={h.hopId}>
                    <line x1={d.x - 150} y1={d.y - 95} x2={d.x - 30} y2={d.y - 20}
                      className={`edge own ${ok ? "verified" : ""}`} />
                    <text x={d.x - 175} y={d.y - 105} className="amt own">{rs(h.amount)} own funds</text>
                  </g>
                );
              })}
              {NODES.map((n) => {
                const lien = nodeLien(n.id);
                return (
                  <g key={n.id} transform={`translate(${n.x},${n.y})`}>
                    <rect x={-70} y={-38} width={140} height={76} rx={12} className={`node ${n.kind}`} />
                    <text y={-14} className="nname">{n.name}</text>
                    <text y={4} className="nsub">{n.sub}</text>
                    {lien != null
                      ? <text y={24} className="nlien">lien {rs(lien)}</text>
                      : <text y={24} className="nsub">{n.kind === "cashout" ? "off-trail" : " "}</text>}
                  </g>
                );
              })}
            </svg>
          </section>

          <aside className="side">
            <div className="card summary">
              <h2>Case summary {trace && <span className="rulechip">{trace.rule}</span>}</h2>
              <div className="metrics">
                <div><b>{rs(8000000)}</b><span>claimed</span></div>
                <div><b>{trace ? rs(trace.totalVerified) : "—"}</b><span>verified in trail</span></div>
                <div className="good"><b>{trace ? rs(trace.totalRecoverable) : "—"}</b><span>recoverable now</span></div>
                <div className="bad"><b>{trace ? rs(trace.totalCashedOut) : "—"}</b><span>cashed out</span></div>
                <div className="good"><b>{trace ? rs(freezeAvoided) : "—"}</b><span>wrongful freeze avoided</span></div>
              </div>
            </div>

            <div className="card">
              <h2>Allocation rule <small>(configurable policy)</small></h2>
              <div className="rules">
                {RULES.map((r) => (
                  <button key={r.id}
                    className={rule === r.id ? "on" : ""}
                    disabled={busy || !kase || attested.length === 0}
                    onClick={() => compute(r.id)}>
                    <b>{r.id}</b><span>{r.desc}</span>
                  </button>
                ))}
              </div>
            </div>

            {trace && (
              <div className="card">
                <h2>Per-victim recovery</h2>
                <table>
                  <thead><tr><th>Victim</th><th>Claimed</th><th>Recoverable</th><th>Cashed out</th></tr></thead>
                  <tbody>
                    {trace.victims.map((v) => (
                      <tr key={v.victimRef}>
                        <td>{Object.keys(TOKENS).find((k) => TOKENS[k] === v.victimRef)}</td>
                        <td>{rs(v.claimed)}</td>
                        <td className="good">{rs(v.recoverable)}</td>
                        <td className="bad">{rs(v.cashedOut)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <details><summary>Tracing notes ({trace.notes.length})</summary>
                  <ul>{trace.notes.map((n, i) => <li key={i}>{n}</li>)}</ul>
                </details>
              </div>
            )}

            {trace && (
              <div className="card actions">
                <h2>Restoration</h2>
                {!proposal && <button className="primary" disabled={busy} onClick={propose}>⑥ Propose restoration ({rule})</button>}
                {proposal && (
                  <>
                    <p className="prop-id">{proposal.actionId} · {proposal.status}</p>
                    <div className="approvals">
                      {proposal.requiredOrgs.map((o) => (
                        <button key={o} disabled={busy || proposal.approvals.includes(o) || proposal.status === "APPROVED"}
                          onClick={() => approve(o)}>
                          {proposal.approvals.includes(o) ? "✓ " : ""}{o}
                        </button>
                      ))}
                    </div>
                    <p className="hint">Majority of {proposal.requiredOrgs.length} orgs required; LEA always included.</p>
                  </>
                )}
              </div>
            )}
          </aside>
        </main>
      )}

      {tab === "ops" && (
        <main className="ops-tab">
          <div className="card">
            <h2>Attestation queue <small>— each bank signs only the legs it handled</small></h2>
            <table>
              <thead><tr><th>Hop</th><th>Time</th><th>Flow</th><th>Amount</th><th>Attested by</th><th></th></tr></thead>
              <tbody>
                {DEMO_HOPS.map((h) => {
                  const ok = attestedSet.has(h.hopId);
                  return (
                    <tr key={h.hopId} className={ok ? "done" : ""}>
                      <td>{h.hopId}</td><td>{h.ts}</td>
                      <td>{h.own ? "own funds → " : ""}{h.src} → {h.dst}{h.cashOut ? " (cash-out)" : ""}<br /><small>{h.label}</small></td>
                      <td>{rs(h.amount)}</td>
                      <td>{ok ? h.srcBank : <i>pending</i>}</td>
                      <td>{!ok && <button disabled={busy} onClick={() => attest(h)}>Attest as {h.srcBank}</button>}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {pendingHops.length > 0 && kase &&
              <button className="primary" disabled={busy} onClick={attestAll}>② Attest all remaining hops</button>}
          </div>

          {trace && (
            <div className="card">
              <h2>Liens <small>— hold only the traced amount (MHA SOP, Jan 2026)</small></h2>
              <table>
                <thead><tr><th>Account</th><th>Bank</th><th>Traced / lienable</th><th>Whole balance</th><th></th></tr></thead>
                <tbody>
                  {trace.accounts.filter((a) => a.lienable > 0).map((a) => {
                    const existing = liens.find((l) => l.acctToken === a.acctToken);
                    const who = Object.keys(TOKENS).find((k) => TOKENS[k] === a.acctToken) || a.acctToken.slice(0, 8);
                    return (
                      <tr key={a.acctToken}>
                        <td>{who}</td><td>{a.bank}</td>
                        <td>{rs(a.lienable)}</td>
                        <td className="bad">{rs(a.remaining)}</td>
                        <td>
                          {!existing && <button disabled={busy} onClick={() => placeLien(a)}>③ Lien {rs(a.lienable)}</button>}
                          {existing && <>
                            <span className={`badge ${existing.status === "ACTIVE" ? "" : "good"}`}>{existing.status}</span>{" "}
                            <small>deadline {new Date(existing.deadline).toLocaleDateString()}</small>{" "}
                            {existing.status === "ACTIVE" &&
                              <button disabled={busy} onClick={() => flagDeadline(existing)}>④ Flag 90-day deadline</button>}
                          </>}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              <p className="hint">Without WAPSI, banks freeze the whole balance ({rs(trace.accounts.reduce((s, a) => s + a.remaining, 0))} across trail accounts). WAPSI freezes only the traced portion.</p>
            </div>
          )}
          <div className="card">
            <h2>Demo script</h2>
            <ol>
              <li>① Create case C001 (from the header)</li>
              <li>② Attest hops — watch edges turn green</li>
              <li>③ Compute trace (Money trail tab) — switch rules, watch recoverable amounts change</li>
              <li>④ Lien only the traced amount; flag the 90-day deadline</li>
              <li>⑤ Propose restoration and approve by majority</li>
            </ol>
          </div>
        </main>
      )}

      {toast && <div className={`toast ${toast.ok ? "" : "err"}`}>{toast.msg}</div>}
    </div>
  );
}
