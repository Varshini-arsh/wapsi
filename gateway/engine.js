/**
 * WAPSI tracing engine — JavaScript mirror of chaincode/tracing.go and
 * simulator/simulate.py. Same rules (LIBR, OWN_FIRST, FIFO, PRORATA), same
 * debit-first composition model, same integer paise math.
 */
"use strict";

const OWN = ""; // composition key for own funds
const OWNFUND = "OWNFUND:";
const CASH_OUT = "CASH_OUT:";
const RULES = ["LIBR", "OWN_FIRST", "FIFO", "PRORATA"];

function isOwnFund(t) { return typeof t === "string" && t.startsWith(OWNFUND); }
function isCashOut(t) { return typeof t === "string" && t.startsWith(CASH_OUT); }

function rs(p) {
  const sign = p < 0 ? "-" : "";
  const abs = Math.abs(p);
  return `${sign}Rs ${Math.floor(abs / 100).toLocaleString("en-IN")}.${String(abs % 100).padStart(2, "0")}`;
}

function newAccount(bank, token) {
  return {
    bank, token, lots: [], balance: 0,
    fraudCredited: 0, ownCredited: 0,
    minBalAfterFraud: 0, sawFraud: false,
    isCashOut: isCashOut(token), isVictimRoot: false, nextSeq: 0,
  };
}

function fraudRemaining(a) {
  return a.lots.reduce((s, l) => (l.victimTk !== OWN ? s + l.amount : s), 0);
}

function buildEngine(hops) {
  if (!hops || hops.length === 0) throw new Error("no hops attested for this case yet");
  const sorted = [...hops].sort((x, y) =>
    x.timestamp < y.timestamp ? -1 : x.timestamp > y.timestamp ? 1 : x.hopId < y.hopId ? -1 : 1);
  const e = { hops: sorted, accts: new Map(), victimOrder: [], rootAmount: new Map(), rootBank: new Map() };
  const receives = new Set(sorted.map((h) => h.dstAcctToken));
  for (const h of sorted) {
    const s = h.srcAcctToken;
    if (isOwnFund(s)) continue;
    if (!receives.has(s) && !isCashOut(s)) {
      if (!e.rootAmount.has(s)) {
        e.rootAmount.set(s, h.amount);
        e.rootBank.set(s, h.srcBank);
        e.victimOrder.push(s);
        acctOf(e, s, h.srcBank).isVictimRoot = true;
      }
    }
  }
  if (e.victimOrder.length === 0) throw new Error("no victim origin hops found: every src account also receives money");
  return e;
}

function acctOf(e, token, bank) {
  if (!e.accts.has(token)) e.accts.set(token, newAccount(bank, token));
  return e.accts.get(token);
}

function trackMin(e, a) {
  if (a.isCashOut || !a.sawFraud || a.isVictimRoot) return;
  if (a.minBalAfterFraud === 0 || a.balance < a.minBalAfterFraud) a.minBalAfterFraud = a.balance;
}

function credit(e, token, bank, comp) {
  const a = acctOf(e, token, bank);
  const keys = Object.keys(comp).sort((x, y) => {
    const ox = x === OWN, oy = y === OWN;
    if (ox !== oy) return oy ? -1 : 1; // fraud lots before the own-funds lot
    return x < y ? -1 : 1;
  });
  for (const vk of keys) {
    const amt = comp[vk];
    if (!(amt > 0)) continue;
    a.nextSeq += 1;
    a.lots.push({ victimTk: vk, amount: amt, seq: a.nextSeq });
    a.balance += amt;
    if (vk === OWN) a.ownCredited += amt;
    else { a.fraudCredited += amt; a.sawFraud = true; }
    trackMin(e, a);
  }
}

function debit(e, a, amount, rule) {
  const comp = {};
  if (amount <= 0 || a.balance <= 0) return comp;
  amount = Math.min(amount, a.balance);
  if (rule === "PRORATA") {
    const total = a.balance;
    let allocated = 0;
    for (const l of a.lots) {
      if (l.amount === 0) continue;
      const share = Math.trunc((amount * l.amount) / total);
      l.amount -= share;
      comp[l.victimTk] = (comp[l.victimTk] || 0) + share;
      allocated += share;
    }
    const leftover = amount - allocated;
    if (leftover > 0) {
      let bi = 0;
      for (let i = 1; i < a.lots.length; i++) if (a.lots[i].amount > a.lots[bi].amount) bi = i;
      a.lots[bi].amount -= leftover;
      comp[a.lots[bi].victimTk] = (comp[a.lots[bi].victimTk] || 0) + leftover;
    }
    a.balance -= amount;
    trackMin(e, a);
    return comp;
  }
  const idx = a.lots.map((_, i) => i);
  if (rule === "OWN_FIRST") {
    idx.sort((x, y) => {
      const lx = a.lots[x], ly = a.lots[y];
      const ox = lx.victimTk === OWN, oy = ly.victimTk === OWN;
      if (ox !== oy) return ox ? -1 : 1; // own funds first
      return lx.seq - ly.seq;
    });
  } else {
    idx.sort((x, y) => a.lots[x].seq - a.lots[y].seq);
  }
  let remaining = amount;
  for (const i of idx) {
    if (remaining <= 0) break;
    const l = a.lots[i];
    if (l.amount <= 0) continue;
    const take = Math.min(l.amount, remaining);
    l.amount -= take;
    remaining -= take;
    comp[l.victimTk] = (comp[l.victimTk] || 0) + take;
  }
  a.balance -= amount;
  trackMin(e, a);
  return comp;
}

function seedRootLot(e, a, token, amount) {
  if (a.isVictimRoot && a.lots.length === 0) {
    a.nextSeq += 1;
    a.lots.push({ victimTk: token, amount, seq: a.nextSeq });
    a.balance += amount;
    a.fraudCredited += amount;
    a.sawFraud = true;
  }
}

function replay(e, rule) {
  for (const h of e.hops) {
    let comp;
    if (isOwnFund(h.srcAcctToken)) {
      comp = { [OWN]: h.amount };
    } else {
      const src = acctOf(e, h.srcAcctToken, h.srcBank);
      seedRootLot(e, src, h.srcAcctToken, h.amount);
      comp = debit(e, src, h.amount, rule);
    }
    if (isCashOut(h.dstAcctToken)) {
      const d = acctOf(e, h.dstAcctToken, h.dstBank);
      for (const [vk, amt] of Object.entries(comp)) {
        if (!(amt > 0)) continue;
        d.nextSeq += 1;
        d.lots.push({ victimTk: vk, amount: amt, seq: d.nextSeq });
        d.balance += amt;
        if (vk === OWN) d.ownCredited += amt;
        else { d.fraudCredited += amt; d.sawFraud = true; }
      }
    } else {
      credit(e, h.dstAcctToken, h.dstBank, comp);
    }
  }
}

function applyLIBRCaps(e, notes) {
  const tokens = [...e.accts.keys()].sort();
  for (const tk of tokens) {
    const a = e.accts.get(tk);
    if (a.isCashOut || a.isVictimRoot || !a.sawFraud) continue;
    const fr = fraudRemaining(a);
    if (fr <= 0) continue;
    const cap = Math.min(a.minBalAfterFraud, a.balance);
    if (fr <= cap) continue;
    let sum = 0;
    for (const l of a.lots) {
      if (l.victimTk !== OWN) {
        l.amount = Math.trunc((l.amount * cap) / fr);
        sum += l.amount;
      }
    }
    const leftover = cap - sum;
    if (leftover > 0) {
      let bi = -1, best = -1;
      a.lots.forEach((l, i) => {
        if (l.victimTk !== OWN && l.amount > best) { best = l.amount; bi = i; }
      });
      if (bi >= 0) a.lots[bi].amount += leftover;
    }
    notes.push(`${tk}: LIBR caps the victim claim at the lowest intermediate balance (${rs(cap)}), reducing it from ${rs(fr)}`);
  }
}

function computeTrace(hops, rule, caseClaimed = 0) {
  if (!RULES.includes(rule)) throw new Error(`invalid rule ${rule}; must be one of LIBR, OWN_FIRST, FIFO, PRORATA`);
  const e = buildEngine(hops);
  replay(e, rule);
  const notes = [];
  if (rule === "LIBR") applyLIBRCaps(e, notes);

  const accounts = [];
  for (const tk of [...e.accts.keys()].sort()) {
    const a = e.accts.get(tk);
    if (a.fraudCredited === 0) continue;
    const fr = fraudRemaining(a);
    accounts.push({
      acctToken: tk, bank: a.bank,
      received: a.fraudCredited, ownFunds: a.ownCredited,
      remaining: a.balance, debited: a.fraudCredited - fr,
      lienable: a.isCashOut ? 0 : fr,
    });
    if (a.isCashOut) {
      notes.push(`${tk}: cash-out sink, ${rs(a.fraudCredited)} left the attested trail; needs off-chain LEA action`);
    }
  }

  const recovered = new Map(), cashed = new Map();
  for (const a of e.accts.values()) {
    if (a.fraudCredited === 0) continue;
    for (const l of a.lots) {
      if (l.victimTk === OWN || l.amount === 0) continue;
      const tgt = a.isCashOut ? cashed : recovered;
      tgt.set(l.victimTk, (tgt.get(l.victimTk) || 0) + l.amount);
    }
  }

  const victims = [];
  let totalRecoverable = 0, totalCashedOut = 0, totalVerified = 0;
  for (const vt of e.victimOrder) {
    const rec = recovered.get(vt) || 0, cas = cashed.get(vt) || 0;
    victims.push({
      victimRef: vt, bank: e.rootBank.get(vt),
      claimed: e.rootAmount.get(vt), verified: e.rootAmount.get(vt),
      recoverable: rec, cashedOut: cas,
    });
    totalRecoverable += rec; totalCashedOut += cas; totalVerified += e.rootAmount.get(vt);
  }

  notes.push({
    LIBR: "LIBR: a victim's claim on an account cannot exceed the lowest balance that account reached after fraud money arrived. Configurable policy for this demo, not a legally mandated rule.",
    OWN_FIRST: "OWN_FIRST: the wrongdoer is assumed to spend legitimate money first, maximising the fraud amount that stays traceable. Configurable policy for this demo.",
    FIFO: "FIFO: money is spent in the order it was credited. Configurable policy for this demo.",
    PRORATA: "PRORATA: every spend is shared proportionally between legitimate and fraud money. Configurable policy for this demo.",
  }[rule]);

  return {
    rule, totalClaimed: caseClaimed, totalVerified,
    totalRecoverable, totalCashedOut, accounts, victims, notes,
  };
}

module.exports = { computeTrace, RULES };
