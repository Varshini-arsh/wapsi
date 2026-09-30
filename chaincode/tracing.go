package main

import (
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// Tracing engine (pure functions, unit-testable without a ledger).
//
// Model: time-ordered replay of attested hops. Every account balance is
// decomposed into "lots"; each lot is either own funds (victimTk == "") or
// money attributable to one victim (victimTk == victim account token).
//
// Debit-first composition: when a hop debits a source account, the engine
// computes exactly WHICH lots the debit consumes (per the allocation rule).
// That composition is what arrives at the destination. This means:
//   - mixed accounts behave correctly (spending legitimate money does not
//     forward fraud money downstream),
//   - no victim's money is ever double-counted,
//   - victim attribution flows through multi-hop trails automatically.
//
// Rules:
//   OWN_FIRST: wrongdoer spends legitimate money first (Re Hallett style).
//   FIFO:      money is spent in credit order.
//   LIBR:      FIFO debit order + per-account cap: total victim claim on an
//              account cannot exceed the lowest balance reached after fraud
//              money arrived (lowest intermediate balance rule). Excess is
//              treated as dissipated by the wrongdoer.
//   PRORATA:   every spend is shared proportionally across all lots, so the
//              account owner and victims lose money in the same proportion.
// ---------------------------------------------------------------------------

const ownFundsKey = "" // composition key for the account owner's own money

const ownFundPrefix = "OWNFUND:"
const cashOutPrefix = "CASH_OUT:"

func isOwnFundToken(tk string) bool {
	return len(tk) >= len(ownFundPrefix) && tk[:len(ownFundPrefix)] == ownFundPrefix
}

func isCashOutToken(tk string) bool {
	return len(tk) >= len(cashOutPrefix) && tk[:len(cashOutPrefix)] == cashOutPrefix
}

// lot is one credited chunk of money in an account.
type lot struct {
	victimTk string // "" = own funds
	amount   int64  // remaining amount
	seq      int    // credit order within the account
}

// accountState is the running state of one account during replay.
type accountState struct {
	bank             string
	lots             []lot
	balance          int64
	fraudCredited    int64 // total fraud money ever credited
	ownCredited      int64
	minBalAfterFraud int64 // lowest balance observed after first fraud credit
	sawFraud         bool
	isCashOut        bool
	isVictimRoot     bool
	nextSeq          int
}

func (a *accountState) fraudRemaining() int64 {
	var n int64
	for _, l := range a.lots {
		if l.victimTk != ownFundsKey {
			n += l.amount
		}
	}
	return n
}

// engine holds replay state for one case.
type engine struct {
	accts       map[string]*accountState
	hops        []TransactionHop
	victimOrder []string          // victim root tokens in first-seen order
	rootAmount  map[string]int64  // victim token -> stolen amount (first hop)
	rootBank    map[string]string // victim token -> victim's bank MSP
}

// buildEngine sorts hops and detects victim roots (accounts that send but
// never receive) and own-funding hops.
func buildEngine(hops []TransactionHop) (*engine, error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("no hops attested for this case yet")
	}
	e := &engine{
		accts:      map[string]*accountState{},
		rootAmount: map[string]int64{},
		rootBank:   map[string]string{},
	}
	e.hops = append(e.hops, hops...)

	// Deterministic replay order: timestamp, then hopId.
	sort.SliceStable(e.hops, func(i, j int) bool {
		if e.hops[i].Timestamp != e.hops[j].Timestamp {
			return e.hops[i].Timestamp < e.hops[j].Timestamp
		}
		return e.hops[i].HopID < e.hops[j].HopID
	})

	receives := map[string]bool{}
	for _, h := range e.hops {
		receives[h.DstAcctToken] = true
	}

	for _, h := range e.hops {
		if isOwnFundToken(h.SrcAcctToken) {
			continue // own-funding source; not an account, not a victim
		}
		if !receives[h.SrcAcctToken] && !isCashOutToken(h.SrcAcctToken) {
			// Victim root: first hop out of an account nobody funded.
			if _, ok := e.rootAmount[h.SrcAcctToken]; !ok {
				e.rootAmount[h.SrcAcctToken] = h.Amount
				e.rootBank[h.SrcAcctToken] = h.SrcBank
				e.victimOrder = append(e.victimOrder, h.SrcAcctToken)
				e.acct(h.SrcAcctToken, h.SrcBank).isVictimRoot = true
			}
		}
	}
	if len(e.victimOrder) == 0 {
		return nil, fmt.Errorf("no victim origin hops found: every src account also receives money")
	}
	return e, nil
}

func (e *engine) acct(tk, bank string) *accountState {
	a, ok := e.accts[tk]
	if !ok {
		a = &accountState{bank: bank, isCashOut: isCashOutToken(tk)}
		e.accts[tk] = a
	}
	return a
}

// trackMin maintains the LIBR lowest-intermediate-balance watermark.
func (e *engine) trackMin(a *accountState) {
	if a.isCashOut || !a.sawFraud || a.isVictimRoot {
		return
	}
	if a.minBalAfterFraud == 0 || a.balance < a.minBalAfterFraud {
		a.minBalAfterFraud = a.balance
	}
}

// credit adds a composition (victimTk -> amount, ownFundsKey for own money)
// to an account as new lots. Fraud lots are ordered before own-funds lots,
// victims in sorted token order, so replay is fully deterministic.
func (e *engine) credit(tk, bank string, comp map[string]int64) {
	a := e.acct(tk, bank)
	keys := make([]string, 0, len(comp))
	for k := range comp {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, oj := keys[i] == ownFundsKey, keys[j] == ownFundsKey
		if oi != oj {
			return oj // fraud lots before the own-funds lot
		}
		return keys[i] < keys[j]
	})
	for _, vk := range keys {
		amt := comp[vk]
		if amt <= 0 {
			continue
		}
		a.nextSeq++
		a.lots = append(a.lots, lot{victimTk: vk, amount: amt, seq: a.nextSeq})
		a.balance += amt
		if vk == ownFundsKey {
			a.ownCredited += amt
		} else {
			a.fraudCredited += amt
			a.sawFraud = true
		}
		e.trackMin(a)
	}
}

// debitOrder returns lot indices in the order the rule consumes them.
func debitOrder(a *accountState, rule string) []int {
	idx := make([]int, len(a.lots))
	for i := range idx {
		idx[i] = i
	}
	switch rule {
	case RuleOwnFirst:
		sort.SliceStable(idx, func(x, y int) bool {
			li, lj := a.lots[idx[x]], a.lots[idx[y]]
			if (li.victimTk == ownFundsKey) != (lj.victimTk == ownFundsKey) {
				return li.victimTk == ownFundsKey // own funds first
			}
			return li.seq < lj.seq
		})
	default: // FIFO, LIBR
		sort.SliceStable(idx, func(x, y int) bool { return a.lots[idx[x]].seq < a.lots[idx[y]].seq })
	}
	return idx
}

// debitComposition removes `amount` from the account per the rule and returns
// the composition of what was removed.
func (e *engine) debitComposition(a *accountState, amount int64, rule string) map[string]int64 {
	comp := map[string]int64{}
	if amount <= 0 || a.balance <= 0 {
		return comp
	}
	if amount > a.balance {
		amount = a.balance // defensive; sane data never overdraws
	}
	if rule == RuleProRata {
		// Every lot loses the same fraction of itself.
		var allocated int64
		for i := range a.lots {
			if a.lots[i].amount == 0 {
				continue
			}
			share := amount * a.lots[i].amount / a.balance
			a.lots[i].amount -= share
			comp[a.lots[i].victimTk] += share
			allocated += share
		}
		// Rounding remainder to the largest lot.
		if leftover := amount - allocated; leftover > 0 {
			bi, best := -1, int64(-1)
			for i := range a.lots {
				if a.lots[i].amount > best {
					best = a.lots[i].amount
					bi = i
				}
			}
			if bi >= 0 {
				a.lots[bi].amount -= leftover
				comp[a.lots[bi].victimTk] += leftover
			}
		}
		a.balance -= amount
		e.trackMin(a)
		return comp
	}
	remaining := amount
	for _, i := range debitOrder(a, rule) {
		if remaining <= 0 {
			break
		}
		l := &a.lots[i]
		if l.amount <= 0 {
			continue
		}
		take := l.amount
		if take > remaining {
			take = remaining
		}
		l.amount -= take
		remaining -= take
		comp[l.victimTk] += take
	}
	a.balance -= amount
	e.trackMin(a)
	return comp
}

// seedRootLot gives a victim-root account its initial stolen-money lot the
// first time it debits (the theft debit out of the victim's account).
func (e *engine) seedRootLot(a *accountState, tk string, amount int64) {
	if a.isVictimRoot && len(a.lots) == 0 {
		a.nextSeq++
		a.lots = append(a.lots, lot{victimTk: tk, amount: amount, seq: a.nextSeq})
		a.balance += amount
		a.fraudCredited += amount
		a.sawFraud = true
	}
}

// replay processes hops in timestamp order.
func (e *engine) replay(rule string) {
	for _, h := range e.hops {
		var comp map[string]int64
		switch {
		case isOwnFundToken(h.SrcAcctToken):
			// Legitimate money entering the trail.
			comp = map[string]int64{ownFundsKey: h.Amount}
		default:
			src := e.acct(h.SrcAcctToken, h.SrcBank)
			e.seedRootLot(src, h.SrcAcctToken, h.Amount)
			comp = e.debitComposition(src, h.Amount, rule)
		}
		if isCashOutToken(h.DstAcctToken) {
			// Cash-out sink: tally what arrived, per victim; lots retained
			// for attribution but the money is treated as gone from the trail.
			dst := e.acct(h.DstAcctToken, h.DstBank)
			for vk, amt := range comp {
				if amt <= 0 {
					continue
				}
				dst.nextSeq++
				dst.lots = append(dst.lots, lot{victimTk: vk, amount: amt, seq: dst.nextSeq})
				dst.balance += amt
				if vk == ownFundsKey {
					dst.ownCredited += amt
				} else {
					dst.fraudCredited += amt
					dst.sawFraud = true
				}
			}
			continue
		}
		e.credit(h.DstAcctToken, h.DstBank, comp)
	}
}

// applyLIBRCaps scales fraud lots down per account so no account's total
// victim claim exceeds min(fraudRemaining, lowest intermediate balance).
func (e *engine) applyLIBRCaps(res *TraceResult) {
	tokens := make([]string, 0, len(e.accts))
	for tk := range e.accts {
		tokens = append(tokens, tk)
	}
	sort.Strings(tokens)
	for _, tk := range tokens {
		a := e.accts[tk]
		if a.isCashOut || a.isVictimRoot || !a.sawFraud {
			continue
		}
		fr := a.fraudRemaining()
		if fr <= 0 {
			continue
		}
		cap := a.minBalAfterFraud
		if a.balance < cap {
			cap = a.balance
		}
		if fr <= cap {
			continue
		}
		// Scale each victim's remaining share down proportionally to cap.
		var sum int64
		for i := range a.lots {
			if a.lots[i].victimTk != ownFundsKey {
				a.lots[i].amount = a.lots[i].amount * cap / fr
				sum += a.lots[i].amount
			}
		}
		if leftover := cap - sum; leftover > 0 {
			bi, best := -1, int64(-1)
			for i := range a.lots {
				if a.lots[i].victimTk != ownFundsKey && a.lots[i].amount > best {
					best = a.lots[i].amount
					bi = i
				}
			}
			if bi >= 0 {
				a.lots[bi].amount += leftover
			}
		}
		res.Notes = append(res.Notes, fmt.Sprintf(
			"%s: LIBR caps the victim claim at the lowest intermediate balance (%s), reducing it from %s",
			tk, paise(cap), paise(fr)))
	}
}

// computeTrace replays the trail under the given rule and produces results.
func computeTrace(hops []TransactionHop, caseClaimed int64, rule string) (*TraceResult, error) {
	if !validRules[rule] {
		return nil, fmt.Errorf("invalid rule %q; must be one of LIBR, OWN_FIRST, FIFO, PRORATA", rule)
	}
	e, err := buildEngine(hops)
	if err != nil {
		return nil, err
	}
	e.replay(rule)

	res := &TraceResult{Rule: rule, TotalClaimed: caseClaimed}

	if rule == RuleLIBR {
		e.applyLIBRCaps(res)
	}

	// Per-account results (accounts that touched fraud money only).
	tokens := make([]string, 0, len(e.accts))
	for tk := range e.accts {
		tokens = append(tokens, tk)
	}
	sort.Strings(tokens)
	for _, tk := range tokens {
		a := e.accts[tk]
		if a.fraudCredited == 0 {
			continue
		}
		at := AccountTrace{
			Bank:      a.bank,
			AcctToken: tk,
			Received:  a.fraudCredited,
			OwnFunds:  a.ownCredited,
			Remaining: a.balance,
		}
		fr := a.fraudRemaining()
		at.Debited = a.fraudCredited - fr
		switch {
		case a.isCashOut:
			at.Lienable = 0
			res.Notes = append(res.Notes, fmt.Sprintf("%s: cash-out sink, %s left the attested trail; needs off-chain LEA action", tk, paise(a.fraudCredited)))
		default:
			at.Lienable = fr // already LIBR-capped where applicable
		}
		res.Accounts = append(res.Accounts, at)
	}

	// Per-victim results.
	victimRecovered := map[string]int64{}
	victimCashed := map[string]int64{}
	for _, a := range e.accts {
		if a.fraudCredited == 0 {
			continue
		}
		for _, l := range a.lots {
			if l.victimTk == ownFundsKey || l.amount == 0 {
				continue
			}
			if a.isCashOut {
				victimCashed[l.victimTk] += l.amount
			} else {
				victimRecovered[l.victimTk] += l.amount
			}
		}
	}
	for _, vt := range e.victimOrder {
		vr := VictimRecovery{
			VictimRef:   vt,
			Bank:        e.rootBank[vt],
			Claimed:     e.rootAmount[vt],
			Verified:    e.rootAmount[vt],
			Recoverable: victimRecovered[vt],
			CashedOut:   victimCashed[vt],
		}
		res.Victims = append(res.Victims, vr)
		res.TotalRecoverable += vr.Recoverable
		res.TotalCashedOut += vr.CashedOut
		res.TotalVerified += vr.Verified
	}

	switch rule {
	case RuleLIBR:
		res.Notes = append(res.Notes, "LIBR: a victim's claim on an account cannot exceed the lowest balance that account reached after fraud money arrived. Configurable policy for this demo, not a legally mandated rule.")
	case RuleOwnFirst:
		res.Notes = append(res.Notes, "OWN_FIRST: the wrongdoer is assumed to spend legitimate money first, maximising the fraud amount that stays traceable. Configurable policy for this demo.")
	case RuleFIFO:
		res.Notes = append(res.Notes, "FIFO: money is spent in the order it was credited. Configurable policy for this demo.")
	case RuleProRata:
		res.Notes = append(res.Notes, "PRORATA: every spend is shared proportionally between legitimate and fraud money. Configurable policy for this demo.")
	}
	return res, nil
}

// paise formats an amount given in paise as a rupee string.
func paise(p int64) string {
	return fmt.Sprintf("Rs %d.%02d", p/100, p%100)
}
