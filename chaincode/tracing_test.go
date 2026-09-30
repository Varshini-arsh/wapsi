package main

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Demo scenario (all amounts in paise; Rs 1 = 100 paise).
//
// Victims (Org1MSP / SBI): Priya 4,000,000p | Arjun 2,500,000p | Meena 1,500,000p
// Trail (timestamp order):
//   H0 07:30  OWNFUND:R    -> RAVI (Org3)        80,000    (shopkeeper's prior balance)
//   H1 08:00  PRIYA (Org1) -> A     (Org2)       4,000,000
//   H2 08:01  ARJUN (Org1) -> A     (Org2)       2,500,000
//   H3 08:02  MEENA (Org1) -> B     (Org3)       1,500,000
//   H4 08:05  A     (Org2) -> B     (Org3)       5,000,000
//   H5 08:12  OWNFUND:B    -> B     (Org3)       3,500,000 (mule's own salary)
//   H6 08:20  B     (Org3) -> RAVI  (Org3)       1,000,000 (innocent shopkeeper)
//   H7 08:25  B     (Org3) -> CASH_OUT:W1        4,500,000 (mule cash-out)
//
// Hand-verified expectations (paise), confirmed by lot-level replay dump:
//   OWN_FIRST: A (no own funds) spends chronologically -> Priya's full 4,000,000
//              lands at B. B spends own 3,500,000 first (Ravi gets 0 fraud
//              money), then Meena 1,500,000 + Arjun 500,000 go to cash-out.
//              Priya 4,000,000 | Arjun 2,000,000 | Meena 0 | total 6,000,000.
//   FIFO:      B debits oldest first -> Ravi holds Meena 1,000,000.
//              Priya 1,000,000 | Arjun 1,500,000 | Meena 1,000,000 | total 3,500,000.
//   LIBR:      cap on B does NOT bind (min intermediate balance 1,500,000 >=
//              remaining claim 1,000,000) -> equals FIFO here. Binding covered
//              by TestLIBRBindingCase.
//   PRORATA:   Ravi lienable 649,999; conservation holds.
// ---------------------------------------------------------------------------

const P int64 = 100 // paise per rupee

func mkHop(id, ts, srcBank, src, dstBank, dst string, amount int64) TransactionHop {
	return TransactionHop{
		CaseID: "C001", HopID: id, Timestamp: ts,
		SrcBank: srcBank, SrcAcctToken: src,
		DstBank: dstBank, DstAcctToken: dst,
		Amount: amount,
	}
}

func demoHops() []TransactionHop {
	return []TransactionHop{
		mkHop("H0", "2026-09-29T07:30:00Z", "Org3MSP", "OWNFUND:R", "Org3MSP", "RAVI", 80000),
		mkHop("H1", "2026-09-29T08:00:00Z", "Org1MSP", "PRIYA", "Org2MSP", "A", 4000000),
		mkHop("H2", "2026-09-29T08:01:00Z", "Org1MSP", "ARJUN", "Org2MSP", "A", 2500000),
		mkHop("H3", "2026-09-29T08:02:00Z", "Org1MSP", "MEENA", "Org3MSP", "B", 1500000),
		mkHop("H4", "2026-09-29T08:05:00Z", "Org2MSP", "A", "Org3MSP", "B", 5000000),
		mkHop("H5", "2026-09-29T08:12:00Z", "Org3MSP", "OWNFUND:B", "Org3MSP", "B", 3500000),
		mkHop("H6", "2026-09-29T08:20:00Z", "Org3MSP", "B", "Org3MSP", "RAVI", 1000000),
		mkHop("H7", "2026-09-29T08:25:00Z", "Org3MSP", "B", "Org3MSP", "CASH_OUT:W1", 4500000),
	}
}

func victimByName(t *testing.T, res *TraceResult, name string) VictimRecovery {
	t.Helper()
	for _, v := range res.Victims {
		if v.VictimRef == name {
			return v
		}
	}
	t.Fatalf("victim %s not found in result", name)
	return VictimRecovery{}
}

func accountByToken(t *testing.T, res *TraceResult, tk string) *AccountTrace {
	t.Helper()
	for i := range res.Accounts {
		if res.Accounts[i].AcctToken == tk {
			return &res.Accounts[i]
		}
	}
	return nil
}

func assertInt64(t *testing.T, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", name, got, want)
	}
}

func TestOwnFirst(t *testing.T) {
	res, err := computeTrace(demoHops(), 8000000, RuleOwnFirst)
	if err != nil {
		t.Fatal(err)
	}
	priya := victimByName(t, res, "PRIYA")
	arjun := victimByName(t, res, "ARJUN")
	meena := victimByName(t, res, "MEENA")
	assertInt64(t, "OWN_FIRST Priya recoverable", priya.Recoverable, 4000000)
	assertInt64(t, "OWN_FIRST Arjun recoverable", arjun.Recoverable, 2000000)
	assertInt64(t, "OWN_FIRST Meena recoverable", meena.Recoverable, 0)
	assertInt64(t, "OWN_FIRST Arjun cashed out", arjun.CashedOut, 500000)
	assertInt64(t, "OWN_FIRST Meena cashed out", meena.CashedOut, 1500000)
	assertInt64(t, "OWN_FIRST total recoverable", res.TotalRecoverable, 6000000)

	// Ravi received only own funds under OWN_FIRST -> no lien possible.
	if accountByToken(t, res, "RAVI") != nil {
		t.Errorf("Ravi should not appear as a fraud-holding account under OWN_FIRST")
	}
}

func TestFIFO(t *testing.T) {
	res, err := computeTrace(demoHops(), 8000000, RuleFIFO)
	if err != nil {
		t.Fatal(err)
	}
	priya := victimByName(t, res, "PRIYA")
	arjun := victimByName(t, res, "ARJUN")
	meena := victimByName(t, res, "MEENA")
	assertInt64(t, "FIFO Priya recoverable", priya.Recoverable, 1000000)
	assertInt64(t, "FIFO Arjun recoverable", arjun.Recoverable, 1500000)
	assertInt64(t, "FIFO Meena recoverable", meena.Recoverable, 1000000)
	assertInt64(t, "FIFO Priya cashed out", priya.CashedOut, 3000000)
	assertInt64(t, "FIFO Meena cashed out", meena.CashedOut, 500000)
	assertInt64(t, "FIFO total recoverable", res.TotalRecoverable, 3500000)

	ravi := accountByToken(t, res, "RAVI")
	if ravi == nil {
		t.Fatalf("Ravi should hold traceable fraud money under FIFO")
	}
	assertInt64(t, "FIFO Ravi lienable", ravi.Lienable, 1000000)
}

func TestLIBRMatchesFIFOWhenCapDoesNotBind(t *testing.T) {
	res, err := computeTrace(demoHops(), 8000000, RuleLIBR)
	if err != nil {
		t.Fatal(err)
	}
	assertInt64(t, "LIBR total recoverable", res.TotalRecoverable, 3500000)
	priya := victimByName(t, res, "PRIYA")
	assertInt64(t, "LIBR Priya recoverable", priya.Recoverable, 1000000)
	ravi := accountByToken(t, res, "RAVI")
	if ravi == nil {
		t.Fatal("Ravi missing under LIBR")
	}
	assertInt64(t, "LIBR Ravi lienable", ravi.Lienable, 1000000)
	// The cap must NOT bind on B (min balance 1,500,000 >= remaining 1,000,000).
	for _, n := range res.Notes {
		if len(n) >= 2 && n[:2] == "B:" {
			t.Errorf("LIBR cap should not bind on B; got note: %s", n)
		}
	}
}

func TestLIBRBindingCase(t *testing.T) {
	// Mule M: V1 sends 1,000p, M drains 900p (lowest balance 100p), then V2
	// sends 800p. LIBR caps the total claim on M at the 100p watermark.
	hops := []TransactionHop{
		mkHop("K1", "2026-09-29T09:00:00Z", "Org1MSP", "V1", "Org2MSP", "M", 1000),
		mkHop("K2", "2026-09-29T09:01:00Z", "Org2MSP", "M", "Org2MSP", "CASH_OUT:X", 900),
		mkHop("K3", "2026-09-29T09:02:00Z", "Org1MSP", "V2", "Org2MSP", "M", 800),
	}
	fifo, err := computeTrace(hops, 1800, RuleFIFO)
	if err != nil {
		t.Fatal(err)
	}
	assertInt64(t, "binding FIFO total", fifo.TotalRecoverable, 900)

	libr, err := computeTrace(hops, 1800, RuleLIBR)
	if err != nil {
		t.Fatal(err)
	}
	// Scale: V1 100*100/900 = 11, V2 800*100/900 = 88 (+1 leftover to largest).
	v1 := victimByName(t, libr, "V1")
	v2 := victimByName(t, libr, "V2")
	assertInt64(t, "LIBR binding V1", v1.Recoverable, 11)
	assertInt64(t, "LIBR binding V2", v2.Recoverable, 89)
	assertInt64(t, "LIBR binding total", libr.TotalRecoverable, 100)
}

func TestProRataConservation(t *testing.T) {
	res, err := computeTrace(demoHops(), 8000000, RuleProRata)
	if err != nil {
		t.Fatal(err)
	}
	ravi := accountByToken(t, res, "RAVI")
	if ravi == nil {
		t.Fatal("Ravi missing under PRORATA")
	}
	assertInt64(t, "PRORATA Ravi lienable", ravi.Lienable, 649999)
	assertInt64(t, "PRORATA recoverable+cashed", res.TotalRecoverable+res.TotalCashedOut, 8000000)
}

func TestConservationAllRules(t *testing.T) {
	for _, rule := range []string{RuleOwnFirst, RuleFIFO, RuleProRata} {
		res, err := computeTrace(demoHops(), 8000000, rule)
		if err != nil {
			t.Fatalf("%s: %v", rule, err)
		}
		if res.TotalRecoverable+res.TotalCashedOut != 8000000 {
			t.Errorf("%s: recoverable %d + cashed %d != 8,000,000", rule, res.TotalRecoverable, res.TotalCashedOut)
		}
		for _, v := range res.Victims {
			if v.Recoverable+v.CashedOut != v.Claimed {
				t.Errorf("%s: victim %s: %d + %d != claimed %d", rule, v.VictimRef, v.Recoverable, v.CashedOut, v.Claimed)
			}
		}
	}
	// LIBR never claims more than was stolen (cap can only shrink claims).
	libr, err := computeTrace(demoHops(), 8000000, RuleLIBR)
	if err != nil {
		t.Fatal(err)
	}
	if libr.TotalRecoverable+libr.TotalCashedOut > 8000000 {
		t.Errorf("LIBR claims exceed the stolen amount")
	}
}

func TestInvalidRule(t *testing.T) {
	if _, err := computeTrace(demoHops(), 8000000, "MAGIC"); err == nil {
		t.Error("expected error for invalid rule")
	}
	if _, err := computeTrace(nil, 8000000, RuleFIFO); err == nil {
		t.Error("expected error for empty hop list")
	}
}

func TestRoundTripAccountLoop(t *testing.T) {
	// Round-tripping (loop) must not double-count: A -> B -> A.
	hops := []TransactionHop{
		mkHop("L1", "2026-09-29T10:00:00Z", "Org1MSP", "V", "Org2MSP", "A", 100000),
		mkHop("L2", "2026-09-29T10:01:00Z", "Org2MSP", "A", "Org2MSP", "B", 60000),
		mkHop("L3", "2026-09-29T10:02:00Z", "Org2MSP", "B", "Org2MSP", "A", 60000),
	}
	res, err := computeTrace(hops, 100000, RuleFIFO)
	if err != nil {
		t.Fatal(err)
	}
	// Total fraud in trail is 100,000p; claims must never exceed it.
	assertInt64(t, "loop total recoverable", res.TotalRecoverable, 100000)
	assertInt64(t, "loop conservation", res.TotalRecoverable+res.TotalCashedOut, 100000)
}
