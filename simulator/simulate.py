#!/usr/bin/env python3
"""
WAPSI simulator & seed generator.

Subcommands:
  seed                      Write seed.json (demo scenario with HMAC acct tokens)
  trace --rule RULE         Read hops JSON from stdin (or use seed hops) and
                            print the TraceResult JSON.

This module mirrors the Go tracing engine in chaincode/tracing.go so the UI
can recompute traces instantly during the demo. The chaincode on Drunix /
Fabric remains the source of truth; agreement between the two is shown by
the identical numbers.

Amounts are in paise (Rs 1 = 100p) everywhere.
"""
import argparse
import hashlib
import hmac
import json
import sys

OWN = ""                     # composition key for own funds
OWNFUND = "OWNFUND:"
CASH_OUT = "CASH_OUT:"
RULES = ("LIBR", "OWN_FIRST", "FIFO", "PRORATA")

# ---------------------------------------------------------------------------
# Privacy: HMAC tokens instead of real account numbers.
# Each bank keeps its own secret and mapping; the ledger only ever sees tokens.
# ---------------------------------------------------------------------------
BANK_SECRETS = {
    "Org1MSP": "sbi-secret-demo-only",
    "Org2MSP": "hdfc-secret-demo-only",
    "Org3MSP": "axis-secret-demo-only",
    "Org4MSP": "lea-secret-demo-only",
}


def token_for(bank: str, acct: str) -> str:
    secret = BANK_SECRETS.get(bank, bank.lower().encode())
    return hmac.new(secret.encode(), acct.encode(), hashlib.sha256).hexdigest()[:16]


# ---------------------------------------------------------------------------
# Demo scenario: 3 victims (Priya 40k, Arjun 25k, Meena 15k) -> mules A, B;
# B mixes 35k legitimate money, pays innocent shopkeeper Ravi 10k, cashes out
# 45k. All amounts in paise.
# ---------------------------------------------------------------------------
DISPLAY = {
    "PRIYA": "Priya (victim, SBI)",
    "ARJUN": "Arjun (victim, SBI)",
    "MEENA": "Meena (victim, SBI)",
    "A": "Mule A (HDFC)",
    "B": "Mule B (Axis, mixed funds)",
    "RAVI": "Ravi (innocent shopkeeper, Axis)",
    "W1": "Mule wallet (cash-out)",
}


def seed_hops():
    """Return demo hops with real HMAC tokens plus display metadata."""
    P = 100
    raw = [
        # hopId, ts, srcBank, srcAcct, dstBank, dstAcct, amount(p), note
        ("H0", "2026-09-29T07:30:00Z", "Org3MSP", "RAVI",  "Org3MSP", "RAVI",  80000,
         "Ravi's opening balance (legitimate)"),
        ("H1", "2026-09-29T08:00:00Z", "Org1MSP", "PRIYA", "Org2MSP", "A", 4000000,
         "Priya defrauded of Rs 40,000"),
        ("H2", "2026-09-29T08:01:00Z", "Org1MSP", "ARJUN", "Org2MSP", "A", 2500000,
         "Arjun defrauded of Rs 25,000"),
        ("H3", "2026-09-29T08:02:00Z", "Org1MSP", "MEENA", "Org3MSP", "B", 1500000,
         "Meena defrauded of Rs 15,000"),
        ("H4", "2026-09-29T08:05:00Z", "Org2MSP", "A",     "Org3MSP", "B", 5000000,
         "Layering: mule A sweeps Rs 50,000 to mule B"),
        ("H5", "2026-09-29T08:12:00Z", "Org3MSP", "B",     "Org3MSP", "B", 3500000,
         "Mule B's own Rs 35,000 salary arrives (mixing)"),
        ("H6", "2026-09-29T08:20:00Z", "Org3MSP", "B",     "Org3MSP", "RAVI", 1000000,
         "Mule B pays Ravi Rs 10,000 (innocent purchase)"),
        ("H7", "2026-09-29T08:25:00Z", "Org3MSP", "B",     "Org3MSP", "W1", 4500000,
         "Mule cash-out of Rs 45,000"),
    ]
    cash_out_dst = {"H7"}  # money leaves the attested trail at these hops
    hops = []
    for hid, ts, sb, sa, db, da, amt, note in raw:
        hops.append({
            "hopId": hid, "timestamp": ts, "note": note,
            "srcBank": sb, "srcAcctId": sa, "dstBank": db, "dstAcctId": da,
            "amount": amt,
            # H0 (Ravi opening balance) and H5 (mule B salary) are legitimate
            # money entering the trail, not fraud transfers.
            "isOwnFunding": hid in ("H0", "H5"),
            # Cash-out destinations are sinks: what arrives there is gone.
            "isCashOutDst": hid in cash_out_dst,
        })
    return hops


def tokenize(hops):
    """Attach HMAC src/dst tokens; own-funding hops get an OWNFUND: pseudo-source."""
    out = []
    for h in hops:
        h = dict(h)
        if h.get("isOwnFunding"):
            h["srcAcctToken"] = OWNFUND + token_for(h["srcBank"], h["srcAcctId"])
        else:
            h["srcAcctToken"] = token_for(h["srcBank"], h["srcAcctId"])
        if h.get("isCashOutDst"):
            h["dstAcctToken"] = CASH_OUT + token_for(h["dstBank"], h["dstAcctId"])
        else:
            h["dstAcctToken"] = token_for(h["dstBank"], h["dstAcctId"])
        out.append(h)
    return out


# ---------------------------------------------------------------------------
# Tracing engine (mirror of chaincode/tracing.go)
# ---------------------------------------------------------------------------
class Account:
    def __init__(self, bank, token):
        self.bank = bank
        self.token = token
        self.lots = []            # dicts: victim, amount, seq
        self.balance = 0
        self.fraud_credited = 0
        self.own_credited = 0
        self.min_bal_after_fraud = 0
        self.saw_fraud = False
        self.is_cash_out = token.startswith(CASH_OUT)
        self.is_victim_root = False
        self.next_seq = 0

    def fraud_remaining(self):
        return sum(l["amount"] for l in self.lots if l["victim"] != OWN)


def rs(p):
    return f"Rs {p // 100:,}.{p % 100:02d}"


class Engine:
    def __init__(self, hops):
        if not hops:
            raise ValueError("no hops attested for this case yet")
        self.hops = sorted(hops, key=lambda h: (h["timestamp"], h["hopId"]))
        self.accts = {}
        self.victim_order = []
        self.root_amount = {}
        self.root_bank = {}
        receives = {h["dstAcctToken"] for h in self.hops}
        for h in self.hops:
            s = h["srcAcctToken"]
            if s.startswith(OWNFUND):
                continue
            if s not in receives and not s.startswith(CASH_OUT):
                if s not in self.root_amount:
                    self.root_amount[s] = h["amount"]
                    self.root_bank[s] = h["srcBank"]
                    self.victim_order.append(s)
                    self.acct(s, h["srcBank"]).is_victim_root = True
        if not self.victim_order:
            raise ValueError("no victim origin hops found")

    def acct(self, token, bank):
        if token not in self.accts:
            self.accts[token] = Account(bank, token)
        return self.accts[token]

    def track_min(self, a):
        if a.is_cash_out or not a.saw_fraud or a.is_victim_root:
            return
        if a.min_bal_after_fraud == 0 or a.balance < a.min_bal_after_fraud:
            a.min_bal_after_fraud = a.balance

    def credit(self, token, bank, comp):
        a = self.acct(token, bank)
        keys = sorted(comp.keys(), key=lambda k: (k == OWN, k))  # fraud first, then own
        for vk in keys:
            amt = comp[vk]
            if amt <= 0:
                continue
            a.next_seq += 1
            a.lots.append({"victim": vk, "amount": amt, "seq": a.next_seq})
            a.balance += amt
            if vk == OWN:
                a.own_credited += amt
            else:
                a.fraud_credited += amt
                a.saw_fraud = True
            self.track_min(a)

    def debit(self, a, amount, rule):
        comp = {}
        if amount <= 0 or a.balance <= 0:
            return comp
        amount = min(amount, a.balance)
        if rule == "PRORATA":
            total = a.balance
            allocated = 0
            for l in a.lots:
                if l["amount"] == 0:
                    continue
                share = amount * l["amount"] // total
                l["amount"] -= share
                comp[l["victim"]] = comp.get(l["victim"], 0) + share
                allocated += share
            leftover = amount - allocated
            if leftover > 0:
                bi = max(range(len(a.lots)), key=lambda i: a.lots[i]["amount"])
                a.lots[bi]["amount"] -= leftover
                comp[a.lots[bi]["victim"]] = comp.get(a.lots[bi]["victim"], 0) + leftover
            a.balance -= amount
            self.track_min(a)
            return comp
        if rule == "OWN_FIRST":
            idx = sorted(range(len(a.lots)),
                         key=lambda i: (a.lots[i]["victim"] != OWN, a.lots[i]["seq"]))
        else:  # FIFO / LIBR
            idx = sorted(range(len(a.lots)), key=lambda i: a.lots[i]["seq"])
        remaining = amount
        for i in idx:
            if remaining <= 0:
                break
            l = a.lots[i]
            if l["amount"] <= 0:
                continue
            take = min(l["amount"], remaining)
            l["amount"] -= take
            remaining -= take
            comp[l["victim"]] = comp.get(l["victim"], 0) + take
        a.balance -= amount
        self.track_min(a)
        return comp

    def replay(self, rule):
        for h in self.hops:
            src = h["srcAcctToken"]
            if src.startswith(OWNFUND):
                comp = {OWN: h["amount"]}
            else:
                a = self.acct(src, h["srcBank"])
                if a.is_victim_root and not a.lots:
                    a.next_seq += 1
                    a.lots.append({"victim": src, "amount": h["amount"], "seq": a.next_seq})
                    a.balance += h["amount"]
                    a.fraud_credited += h["amount"]
                    a.saw_fraud = True
                comp = self.debit(a, h["amount"], rule)
            dst = h["dstAcctToken"]
            if dst.startswith(CASH_OUT):
                d = self.acct(dst, h["dstBank"])
                for vk, amt in comp.items():
                    if amt <= 0:
                        continue
                    d.next_seq += 1
                    d.lots.append({"victim": vk, "amount": amt, "seq": d.next_seq})
                    d.balance += amt
                    if vk == OWN:
                        d.own_credited += amt
                    else:
                        d.fraud_credited += amt
                        d.saw_fraud = True
            else:
                self.credit(dst, h["dstBank"], comp)

    def apply_libr_caps(self, notes):
        for tk in sorted(self.accts):
            a = self.accts[tk]
            if a.is_cash_out or a.is_victim_root or not a.saw_fraud:
                continue
            fr = a.fraud_remaining()
            if fr <= 0:
                continue
            cap = min(a.min_bal_after_fraud, a.balance)
            if fr <= cap:
                continue
            s = 0
            for l in a.lots:
                if l["victim"] != OWN:
                    l["amount"] = l["amount"] * cap // fr
                    s += l["amount"]
            leftover = cap - s
            if leftover > 0:
                idxs = [i for i, l in enumerate(a.lots) if l["victim"] != OWN]
                bi = max(idxs, key=lambda i: a.lots[i]["amount"])
                a.lots[bi]["amount"] += leftover
            notes.append(f"{tk}: LIBR caps the victim claim at the lowest "
                         f"intermediate balance ({rs(cap)}), reducing it from {rs(fr)}")

    def trace(self, rule, claimed=0):
        if rule not in RULES:
            raise ValueError(f"invalid rule {rule!r}")
        self.replay(rule)
        notes = []
        if rule == "LIBR":
            self.apply_libr_caps(notes)

        accounts = []
        for tk in sorted(self.accts):
            a = self.accts[tk]
            if a.fraud_credited == 0:
                continue
            fr = a.fraud_remaining()
            at = {
                "acctToken": tk, "bank": a.bank,
                "received": a.fraud_credited, "ownFunds": a.own_credited,
                "remaining": a.balance, "debited": a.fraud_credited - fr,
                "lienable": 0 if a.is_cash_out else fr,
            }
            if a.is_cash_out:
                notes.append(f"{tk}: cash-out sink, {rs(a.fraud_credited)} left the "
                             f"attested trail; needs off-chain LEA action")
            accounts.append(at)

        recovered, cashed = {}, {}
        for a in self.accts.values():
            if a.fraud_credited == 0:
                continue
            for l in a.lots:
                if l["victim"] == OWN or l["amount"] == 0:
                    continue
                tgt = cashed if a.is_cash_out else recovered
                tgt[l["victim"]] = tgt.get(l["victim"], 0) + l["amount"]

        victims, tot_rec, tot_cash, tot_ver = [], 0, 0, 0
        for vt in self.victim_order:
            rec = recovered.get(vt, 0)
            cas = cashed.get(vt, 0)
            victims.append({
                "victimRef": vt, "bank": self.root_bank[vt],
                "claimed": self.root_amount[vt], "verified": self.root_amount[vt],
                "recoverable": rec, "cashedOut": cas,
            })
            tot_rec += rec
            tot_cash += cas
            tot_ver += self.root_amount[vt]

        notes.append({
            "LIBR": "LIBR: a victim's claim on an account cannot exceed the lowest "
                    "balance that account reached after fraud money arrived. Configurable "
                    "policy for this demo, not a legally mandated rule.",
            "OWN_FIRST": "OWN_FIRST: the wrongdoer is assumed to spend legitimate money "
                         "first, maximising the fraud amount that stays traceable. "
                         "Configurable policy for this demo.",
            "FIFO": "FIFO: money is spent in the order it was credited. Configurable "
                    "policy for this demo.",
            "PRORATA": "PRORATA: every spend is shared proportionally between legitimate "
                       "and fraud money. Configurable policy for this demo.",
        }[rule])
        return {
            "rule": rule, "totalClaimed": claimed,
            "totalVerified": tot_ver, "totalRecoverable": tot_rec,
            "totalCashedOut": tot_cash, "accounts": accounts,
            "victims": victims, "notes": notes,
        }


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------
def cmd_seed(args):
    hops = tokenize(seed_hops())
    acct_index = {}
    for h in hops:
        for side in ("src", "dst"):
            tk, ident, bank = h[f"{side}AcctToken"], h[f"{side}AcctId"], h[f"{side}Bank"]
            if tk.startswith(OWNFUND):
                continue
            if tk.startswith(CASH_OUT):
                ident = tk[len(CASH_OUT):]
                display = "[cash-out wallet] " + DISPLAY.get(ident, ident)
            acct_index.setdefault(tk, {
                "acctId": ident, "bank": bank,
                "display": display if tk.startswith(CASH_OUT) else DISPLAY.get(ident, ident),
            })
    seed = {
        "case": {
            "caseId": "C001", "complaintRef": "NCRP-2026-0931117",
            "victimBank": "Org1MSP", "claimedAmount": 8000000,
            "reportedAt": "2026-09-29T09:05:00Z",
        },
        "accounts": acct_index,
        "hops": hops,
    }
    with open("seed.json", "w", encoding="utf-8") as f:
        json.dump(seed, f, indent=2)
    print(f"wrote seed.json with {len(hops)} hops, "
          f"{len(acct_index)} accounts (HMAC-tokenised)")


def cmd_trace(args):
    raw = sys.stdin.read().strip()
    if raw:
        hops = json.loads(raw)
    else:
        with open("seed.json", encoding="utf-8") as f:
            hops = json.load(f)["hops"]
    res = Engine(hops).trace(args.rule, claimed=8000000)
    json.dump(res, sys.stdout, indent=2)
    print()


def main():
    ap = argparse.ArgumentParser(description="WAPSI tracing simulator")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("seed", help="write seed.json for the demo scenario")
    tp = sub.add_parser("trace", help="trace hops from stdin (or seed.json)")
    tp.add_argument("--rule", default="LIBR", choices=RULES)
    args = ap.parse_args()
    if args.cmd == "seed":
        cmd_seed(args)
    else:
        cmd_trace(args)


if __name__ == "__main__":
    main()
