# WAPSI — Verifiable Real-Time Fund Provenance and Restoration for UPI Cyber Fraud

> UPI moves money in seconds. Fraud recovery still takes months.
> **WAPSI makes recovery move at payment speed.**

A permissioned inter-bank network (Drunix-compatible, deployed on Hyperledger
Fabric v2.5) for multi-hop fraud tracing, proportional
liens and evidence-backed victim restoration.
**Drunix Hackathon x Citi (CHL-7007) · Problem Statement 1 — Real-Time Payments**
(secondary: Financial Inclusion).

---

## The problem

UPI fraud money moves through 3–6 mule accounts at different banks within
minutes. Each bank sees only its own step, so:

1. Victims wait months for refunds — nobody can easily prove the full trail.
2. Innocent people further down the chain get **whole accounts frozen**.
3. When a mule mixes several victims' money with legitimate money, there is no
   transparent way to compute each victim's recoverable share.
4. Hold deadlines aren't tracked across banks.

**Why now:** Supreme Court suo motu WP (Crl) 3/2025 (order dated 4 Aug 2026)
directed RBI to issue an SOP on mule accounts; the MHA SOP (Jan 2026) requires
**holding only the traced amount (lien)** and releasing sub-₹50k holds within
90 days unless extended by a court.

**Positioning (complementary, not competing):** CFCFRMS/I4C reports and routes
complaints; MuleHunter.AI *detects* mule accounts. WAPSI answers the question
neither answers: *"Where did this ₹40,000 go, how much is still recoverable,
who holds it, and what action is allowed next?"* WAPSI is the verifiable
multi-bank provenance + tracing layer beneath CFCFRMS / the Money Restoration
Module.

## What WAPSI does

- Every bank **signs only the transfer steps it handled** (`AttestHop`).
  The ledger combines these into a shared, tamper-evident money trail no single
  bank controls.
- A deterministic **tracing engine** replays the trail as a time-ordered
  transaction graph and computes how much stolen money remains in each
  downstream account — including mixed accounts — under a selectable
  allocation rule:
  - **LIBR** — Lowest Intermediate Balance Rule (claim capped at the lowest
    balance reached after fraud money arrived)
  - **OWN_FIRST** — wrongdoer spends legitimate money first
  - **FIFO** — spend in credit order
  - **PRORATA** — proportional sharing
- Chaincode **never moves real money**. It records facts, computes, collects
  approvals and outputs *authorised action proposals* (lien / release /
  refund). Banks execute the real action.
- **Privacy:** no real account numbers touch the ledger — every account is an
  HMAC(bankSecret, accountId) token; each bank keeps its own mapping.

These rules are **configurable policies for the demo, not a claim that any one
of them is the legally mandated rule**.

## Architecture

```
                 ┌───────────────────────────────────────────────┐
                 │  Permissioned DLT network (channel wapsich)   │
                 │  Target: Drunix · Live demo: Fabric v2.5      │
                 │  test-network fallback (same chaincode)       │
                 │                                               │
  Org1MSP SBI ───┤  chaincode "wapsi" (Go)                       │
  Org2MSP HDFC ──┤   CreateCase · AttestHop · ComputeTrace       │
  Org3MSP Axis ──┤   RecordLien · FlagDeadline                   │
  Org4MSP LEA ───┤   ProposeRestoration · ApproveAction          │
                 │   + pure tracing engine (LIBR/OWN_FIRST/FIFO/ │
                 │     PRORATA), integer-paise math, no floats   │
                 └───────────────▲───────────────────────────────┘
                                 │ fabric-gateway
                 ┌───────────────┴───────────────┐
                 │  gateway (Node/Express REST)  │
                 │  demo mode (in-memory) or     │
                 │  fabric mode (live network)   │
                 └───────────────▲───────────────┘
                                 │ REST + X-WAPSI-MSP
                 ┌───────────────┴───────────────┐
                 │  UI (React + Vite, SVG trail) │
                 │  graph · rule switcher · ops  │
                 └───────────────────────────────┘
```

The demo network runs **two orgs** (Org1MSP = victims' bank, Org2MSP =
downstream/mule side); Org3MSP/Org4MSP (Axis, LEA) are in the chaincode's org
model and scenario narrative — the four-org Drunix topology is the production
target. The chaincode enforces per-bank attestation by MSP identity.

## Repo layout

| Path | What |
|---|---|
| `chaincode/` | Go chaincode (7 core functions + queries) with unit tests |
| `gateway/` | Node/Express REST gateway (demo + fabric modes) |
| `ui/` | React (Vite) console: money-trail graph, rule switcher, ops view |
| `simulator/` | Python seed generator + engine mirror (`python simulate.py seed`) |
| `network/` | Drunix test-network bootstrap (`setup.sh`) + Fabric fallback |

## Quickstart (demo mode — no blockchain required)

```bash
# 1. Gateway
cd gateway && npm install && npm start          # http://localhost:4000

# 2. UI (new terminal)
cd ui && npm install && npm run dev             # http://localhost:5173
```

In the UI: **① Create case C001 → ② Attest all hops (Bank ops tab) →
③ switch allocation rules on the Money trail tab → ③/④ liens + deadline →
⑤ propose restoration → approve by majority.**

## Full stack: bring-up

```bash
# Docker must be running (WSL2 on Windows)
cd network && ./setup.sh                 # Drunix test network (target) + deploy chaincode
FABRIC_FALLBACK=1 ./network/setup.sh     # or: Fabric test-network fallback (used for the live demo)
cd ../gateway && WAPSI_MODE=fabric npm start
```

The **live demo currently runs on the Fabric test-network fallback** — same
chaincode, same APIs; Drunix is the target platform and deploys the identical
chaincode via `./setup.sh`. See `network/README.md` for Drunix-specific
notes (Lite/Committing peer split, YugabyteDB state store, peer index gotcha).

## Demo scenario (seeded)

3 victims — Priya ₹40,000, Arjun ₹25,000, Meena ₹15,000 — money flows through
mule A (HDFC) into mixed mule B (Axis: ₹35,000 legitimate salary), B pays
innocent shopkeeper Ravi ₹10,000 and cashes out ₹45,000.

| Rule | Recoverable total | Priya | Arjun | Meena | Ravi lien |
|---|---|---|---|---|---|
| OWN_FIRST | ₹60,000 | ₹40,000 | ₹20,000 | ₹0 | ₹0 (only own funds) |
| FIFO = LIBR* | ₹35,000 | ₹10,000 | ₹15,000 | ₹10,000 | ₹10,000 |
| PRORATA | ₹50,750 | ₹26,153.84 | ₹16,346.16 | ₹8,250 | ₹6,499.99 |

\* on this scenario the LIBR cap (min intermediate balance ₹15,000 at B) does
not bind; a binding-cap case is unit-tested (`TestLIBRBindingCase`).
Conservation holds under every rule: recoverable + cashed-out = ₹80,000.

The demo shows the **same case under different rules** with recoverable
amounts changing live, and the **"wrongful freezing avoided"** metric:
banks freeze only the traced amount, never the whole account (MHA SOP).

## Proposal form (ready to paste)

**1. Proposal Title**
WAPSI — Verifiable Real-Time Fund Provenance and Restoration for UPI Cyber Fraud

**2. Problem Understanding**
UPI moves money in seconds, but recovering fraud money takes months. Stolen
funds hop through 3–6 mule accounts across banks within minutes; each bank
sees only its own leg, so victims can't be refunded quickly, innocent
downstream account holders get wholly frozen, mixed accounts defeat
proportional recovery, and hold deadlines aren't tracked across institutions.
The Supreme Court (4 Aug 2026) and the MHA SOP (Jan 2026) now require
traced-amount liens and 90-day deadlines — but no infrastructure exists to
compute and prove the traced amount across banks in near real time.

**3. Solution Description**
WAPSI is a permissioned inter-bank network for cross-bank fraud provenance,
built on Drunix-compatible Fabric v2.5 semantics. Banks cryptographically
attest only the transfer legs they handled; the ledger assembles a
tamper-evident, multi-bank money trail. A deterministic tracing engine
replays the trail in time order and computes, per account and per victim, how
much fraud money remains — handling mixed (legitimate + fraud) accounts under
configurable allocation rules (LIBR, own-money-first, FIFO, pro-rata). The
chaincode never moves money: it caps liens at the traced amount, tracks 90-day
deadlines, and collects multi-party approvals for restoration actions that
banks execute. Account privacy is preserved via per-bank HMAC tokens.

**4. Implementation Approach**
Fabric-v2.5-compatible Go chaincode (works on Drunix unmodified) with the
seven core functions; endorsement restricted to the attesting bank; majority
approval including the LEA for restoration. Node/Express gateway using
fabric-gateway; React console with a live money-trail graph and rule
switcher; Python simulator for reproducible scenarios. Tracing logic is
unit-tested and cross-validated across three independent implementations
(Go, Python, JavaScript) to the paisa. Live demo runs on a Hyperledger
Fabric v2.5 test-network fallback; the same chaincode deploys unmodified on
Drunix (target platform) via one `network.sh` flag.

**5. Technology Stack**
Hyperledger Fabric v2.5.x (Drunix-compatible; live demo on the Fabric
test-network fallback) · Go chaincode (fabric-contract-api) · Node.js/Express +
fabric-gateway · React + Vite · Python · Docker · HMAC-sha256 account
tokenization; integer-paise arithmetic throughout (no floating-point money).

**6. Expected Impact**
Targets (not proven results): sub-₹50,000 fraud restoration in days instead
of months by making the traced amount provable within hours; wrongful
freezing reduced to the traced amount only (demo shows ₹X of innocent balance
unfrozen per case); a signed, court-ready, per-hop audit trail; direct
operational support for the MHA SOP (Jan 2026) and the Supreme Court's
Aug 2026 directive; designed to sit beneath CFCFRMS/Money Restoration Module
and extendable to NPCI's UDIR flows.

**7. GitHub Repository URL**
`<add after push>`

**8. Pitch Deck URL**
`<add after export>`

## Testing

```bash
cd chaincode && go test ./...       # tracing engine: per-rule amounts, LIBR
                                    # binding case, conservation, loops
cd simulator && python simulate.py seed && python simulate.py trace --rule LIBR
cd gateway && node -e "require('./engine')"   # engine mirror used in demo mode
```

## Roadmap

1. CFCFRMS / NCRP integration (complaintRef → case auto-creation)
2. Per-bank chaincode-level endorsement policies (currently enforced in code)
3. NPCI UDIR / dispute-resolution hooks; citizen status view
4. Mule-risk scoring hook (MuleHunter) as an advisory signal in proposals

## Team

`<add names/roles>`

---

*WAPSI = "wapsi" (वापसी) — "return". Money comes back at payment speed.*
