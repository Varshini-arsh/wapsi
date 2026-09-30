package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

// SmartContract implements the WAPSI fraud-provenance contract.
// Chaincode NEVER moves real money: it records facts, computes traces and
// collects approvals for action proposals that banks execute off-chain.
type SmartContract struct {
	contractapi.Contract
}

// ---------- helpers ----------

// clientMSP returns the MSP ID of the transaction submitter.
func clientMSP(ctx contractapi.TransactionContextInterface) (string, error) {
	id, err := ctx.GetClientIdentity().GetMSPID()
	if err != nil {
		return "", fmt.Errorf("failed to get client MSP: %v", err)
	}
	return id, nil
}

// nowRFC3339 returns the transaction timestamp (deterministic across endorsers).
func nowRFC3339(ctx contractapi.TransactionContextInterface) (string, error) {
	ts, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return "", err
	}
	return time.Unix(ts.Seconds, int64(ts.Nanos)).UTC().Format(ledgerTimeFormat), nil
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// ---------- 1. CreateCase ----------

// CreateCase opens a fraud case. Callable by any member (typically the
// victim's bank or the LEA after a 1930/NCRP complaint).
func (s *SmartContract) CreateCase(ctx contractapi.TransactionContextInterface,
	caseID, complaintRef, victimBank string, claimedAmount int64, reportedAt string) (*FraudCase, error) {

	if caseID == "" || complaintRef == "" || victimBank == "" {
		return nil, fmt.Errorf("caseId, complaintRef and victimBank are required")
	}
	if claimedAmount <= 0 {
		return nil, fmt.Errorf("claimedAmount must be positive")
	}
	if _, err := parseTime(reportedAt); err != nil {
		return nil, fmt.Errorf("reportedAt must be RFC3339: %v", err)
	}
	existing, _ := ctx.GetStub().GetState(keyCasePrefix + caseID)
	if existing != nil {
		return nil, fmt.Errorf("case %s already exists", caseID)
	}
	msp, err := clientMSP(ctx)
	if err != nil {
		return nil, err
	}
	c := FraudCase{
		DocType:       "fraudcase",
		CaseID:        caseID,
		ComplaintRef:  complaintRef,
		VictimBank:    victimBank,
		ClaimedAmount: claimedAmount,
		ReportedAt:    reportedAt,
		Status:        StatusOpen,
		CreatedBy:     msp,
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := ctx.GetStub().PutState(keyCasePrefix+caseID, b); err != nil {
		return nil, err
	}
	return &c, nil
}

// ---------- 2. AttestHop ----------

// AttestHop records one transfer leg. ONLY the bank that handled the debit
// side (srcBank) may attest it; the signer identity is stored permanently.
func (s *SmartContract) AttestHop(ctx contractapi.TransactionContextInterface,
	caseID, hopID, srcBank, srcAcctToken, dstBank, dstAcctToken string,
	amount int64, timestamp, paymentRef string) (*TransactionHop, error) {

	if caseID == "" || hopID == "" {
		return nil, fmt.Errorf("caseId and hopId are required")
	}
	if srcBank == "" || srcAcctToken == "" || dstBank == "" || dstAcctToken == "" {
		return nil, fmt.Errorf("srcBank, srcAcctToken, dstBank and dstAcctToken are required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if _, err := parseTime(timestamp); err != nil {
		return nil, fmt.Errorf("timestamp must be RFC3339: %v", err)
	}
	caseBytes, err := ctx.GetStub().GetState(keyCasePrefix + caseID)
	if err != nil || caseBytes == nil {
		return nil, fmt.Errorf("case %s not found", caseID)
	}
	msp, err := clientMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != srcBank {
		return nil, fmt.Errorf("only the source bank (%s) may attest this hop; caller is %s", srcBank, msp)
	}
	key, _ := ctx.GetStub().CreateCompositeKey(keyHopPrefix, []string{caseID, hopID})
	existing, _ := ctx.GetStub().GetState(key)
	if existing != nil {
		return nil, fmt.Errorf("hop %s already attested for case %s", hopID, caseID)
	}
	now, err := nowRFC3339(ctx)
	if err != nil {
		return nil, err
	}
	h := TransactionHop{
		DocType:      "hop",
		CaseID:       caseID,
		HopID:        hopID,
		SrcBank:      srcBank,
		SrcAcctToken: srcAcctToken,
		DstBank:      dstBank,
		DstAcctToken: dstAcctToken,
		Amount:       amount,
		Timestamp:    timestamp,
		PaymentRef:   paymentRef,
		AttestedBy:   msp,
		AttestedAt:   now,
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	if err := ctx.GetStub().PutState(key, b); err != nil {
		return nil, err
	}
	return &h, nil
}

// getCaseHops loads every attested hop of a case in hop-id order (the
// engine re-sorts by timestamp).
func (s *SmartContract) getCaseHops(ctx contractapi.TransactionContextInterface, caseID string) ([]TransactionHop, error) {
	iter, err := ctx.GetStub().GetStateByPartialCompositeKey(keyHopPrefix, []string{caseID})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var hops []TransactionHop
	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return nil, err
		}
		var h TransactionHop
		if err := json.Unmarshal(kv.Value, &h); err != nil {
			return nil, err
		}
		hops = append(hops, h)
	}
	return hops, nil
}

// ---------- 3. ComputeTrace ----------

// ComputeTrace replays the attested hops under the chosen allocation rule,
// stores the result on the ledger and returns it.
func (s *SmartContract) ComputeTrace(ctx contractapi.TransactionContextInterface,
	caseID, rule string) (*TraceResult, error) {

	cBytes, err := ctx.GetStub().GetState(keyCasePrefix + caseID)
	if err != nil || cBytes == nil {
		return nil, fmt.Errorf("case %s not found", caseID)
	}
	hops, err := s.getCaseHops(ctx, caseID)
	if err != nil {
		return nil, err
	}
	res, err := computeTrace(hops, 0, rule)
	if err != nil {
		return nil, err
	}
	// TotalClaimed from the case record.
	var fc FraudCase
	_ = json.Unmarshal(cBytes, &fc)
	res.TotalClaimed = fc.ClaimedAmount

	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	if err := ctx.GetStub().PutState("trace_"+caseID, b); err != nil {
		return nil, err
	}
	_ = s.setStatus(ctx, caseID, StatusTraced)
	return res, nil
}

// GetTrace returns the last stored trace for a case (read-only).
func (s *SmartContract) GetTrace(ctx contractapi.TransactionContextInterface, caseID string) (*TraceResult, error) {
	b, err := ctx.GetStub().GetState("trace_" + caseID)
	if err != nil || b == nil {
		return nil, fmt.Errorf("no trace stored for case %s; call ComputeTrace first", caseID)
	}
	var res TraceResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *SmartContract) setStatus(ctx contractapi.TransactionContextInterface, caseID, status string) error {
	cBytes, err := ctx.GetStub().GetState(keyCasePrefix + caseID)
	if err != nil || cBytes == nil {
		return fmt.Errorf("case %s not found", caseID)
	}
	var fc FraudCase
	if err := json.Unmarshal(cBytes, &fc); err != nil {
		return err
	}
	fc.Status = status
	b, _ := json.Marshal(fc)
	return ctx.GetStub().PutState(keyCasePrefix+caseID, b)
}

// ---------- 4. RecordLien ----------

// RecordLien places a hold on the traced portion of an account.
// Callable by the named bank or the LEA; rejects liens above the traced
// amount so innocent money is never frozen beyond the evidence.
func (s *SmartContract) RecordLien(ctx contractapi.TransactionContextInterface,
	caseID, bank, acctToken string, lienAmount int64) (*Lien, error) {

	if lienAmount <= 0 {
		return nil, fmt.Errorf("lienAmount must be positive")
	}
	traceBytes, err := ctx.GetStub().GetState("trace_" + caseID)
	if err != nil || traceBytes == nil {
		return nil, fmt.Errorf("no trace stored for case %s; run ComputeTrace first", caseID)
	}
	var res TraceResult
	if err := json.Unmarshal(traceBytes, &res); err != nil {
		return nil, err
	}
	var matched *AccountTrace
	for i := range res.Accounts {
		if res.Accounts[i].AcctToken == acctToken {
			matched = &res.Accounts[i]
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("account %s not part of the traced trail for case %s", acctToken, caseID)
	}
	if lienAmount > matched.Lienable {
		return nil, fmt.Errorf("lien Rs %d exceeds traced recoverable Rs %d for %s; hold only the traced amount (MHA SOP)", lienAmount, matched.Lienable, acctToken)
	}
	msp, err := clientMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != bank && msp != leaMSP {
		return nil, fmt.Errorf("lien must be recorded by the holding bank (%s) or the LEA (%s); caller is %s", bank, leaMSP, msp)
	}
	now, err := nowRFC3339(ctx)
	if err != nil {
		return nil, err
	}
	deadline, _ := parseTime(now)
	deadline = deadline.AddDate(0, 0, lienDeadlineDays)
	lienID := fmt.Sprintf("L%s-%s", caseID, acctToken[:min(8, len(acctToken))])
	lien := Lien{
		DocType:    "lien",
		CaseID:     caseID,
		LienID:     lienID,
		Bank:       bank,
		AcctToken:  acctToken,
		LienAmount: lienAmount,
		RecordedAt: now,
		Deadline:   deadline.Format(ledgerTimeFormat),
		Status:     StatusLienActive,
		RecordedBy: msp,
	}
	key, _ := ctx.GetStub().CreateCompositeKey(keyLienPrefix, []string{caseID, lienID})
	b, _ := json.Marshal(lien)
	if err := ctx.GetStub().PutState(key, b); err != nil {
		return nil, err
	}
	_ = s.setStatus(ctx, caseID, StatusLiensRecorded)
	return &lien, nil
}

// GetLiens lists liens for a case (read-only).
func (s *SmartContract) GetLiens(ctx contractapi.TransactionContextInterface, caseID string) ([]*Lien, error) {
	iter, err := ctx.GetStub().GetStateByPartialCompositeKey(keyLienPrefix, []string{caseID})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []*Lien
	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return nil, err
		}
		var l Lien
		if err := json.Unmarshal(kv.Value, &l); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, nil
}

// ---------- 5. FlagDeadline ----------

// FlagDeadline computes the 90-day release deadline for a lien (MHA SOP,
// Jan 2026) and marks it RELEASE_ELIGIBLE if the deadline has passed and no
// court extension was recorded. It never auto-releases; the bank executes.
// Optional 2nd arg `asOf` (RFC3339) lets the demo simulate time travel.
func (s *SmartContract) FlagDeadline(ctx contractapi.TransactionContextInterface,
	caseID, lienID string, asOf ...string) (*Lien, error) {

	key, _ := ctx.GetStub().CreateCompositeKey(keyLienPrefix, []string{caseID, lienID})
	b, err := ctx.GetStub().GetState(key)
	if err != nil || b == nil {
		return nil, fmt.Errorf("lien %s not found for case %s", lienID, caseID)
	}
	var l Lien
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	if l.Status == StatusLienExtended {
		return &l, nil
	}
	checkAt := time.Now().UTC()
	if len(asOf) > 0 && asOf[0] != "" {
		var err error
		if checkAt, err = parseTime(asOf[0]); err != nil {
			return nil, fmt.Errorf("asOf must be RFC3339: %v", err)
		}
	}
	deadline, err := parseTime(l.Deadline)
	if err != nil {
		return nil, err
	}
	if !checkAt.Before(deadline) {
		l.Status = StatusLienReleaseEligible
		out, _ := json.Marshal(l)
		if err := ctx.GetStub().PutState(key, out); err != nil {
			return nil, err
		}
	}
	return &l, nil
}

// ExtendLien records a court-ordered extension (optional helper).
func (s *SmartContract) ExtendLien(ctx contractapi.TransactionContextInterface,
	caseID, lienID, newDeadline string) (*Lien, error) {

	key, _ := ctx.GetStub().CreateCompositeKey(keyLienPrefix, []string{caseID, lienID})
	b, err := ctx.GetStub().GetState(key)
	if err != nil || b == nil {
		return nil, fmt.Errorf("lien %s not found", lienID)
	}
	var l Lien
	_ = json.Unmarshal(b, &l)
	if _, err := parseTime(newDeadline); err != nil {
		return nil, fmt.Errorf("newDeadline must be RFC3339")
	}
	msp, err := clientMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != leaMSP {
		return nil, fmt.Errorf("only the LEA (%s) may record a court extension", leaMSP)
	}
	l.Deadline = newDeadline
	l.Status = StatusLienExtended
	out, _ := json.Marshal(l)
	return &l, ctx.GetStub().PutState(key, out)
}

// ---------- 6. ProposeRestoration ----------

// ProposeRestoration creates a restoration proposal from the stored trace.
// Required approvers: every bank that attested a hop in the trail plus the
// LEA; approval passes on a simple majority.
func (s *SmartContract) ProposeRestoration(ctx contractapi.TransactionContextInterface,
	caseID, rule string) (*ActionProposal, error) {

	traceBytes, err := ctx.GetStub().GetState("trace_" + caseID)
	if err != nil || traceBytes == nil {
		return nil, fmt.Errorf("no trace stored for case %s; run ComputeTrace first", caseID)
	}
	var res TraceResult
	if err := json.Unmarshal(traceBytes, &res); err != nil {
		return nil, err
	}
	if rule != "" && rule != res.Rule {
		// Recompute if a different rule is requested.
		fresh, err := s.ComputeTrace(ctx, caseID, rule)
		if err != nil {
			return nil, err
		}
		res = *fresh
	}
	// Collect distinct banks in the trail + LEA.
	orgSet := map[string]bool{leaMSP: true}
	hops, err := s.getCaseHops(ctx, caseID)
	if err != nil {
		return nil, err
	}
	for _, h := range hops {
		orgSet[h.SrcBank] = true
		orgSet[h.DstBank] = true
	}
	var orgs []string
	for o := range orgSet {
		orgs = append(orgs, o)
	}
	now, err := nowRFC3339(ctx)
	if err != nil {
		return nil, err
	}
	actionID := fmt.Sprintf("A%s-%d", caseID, nowUnix(now))
	prop := ActionProposal{
		DocType:      "proposal",
		CaseID:       caseID,
		ActionID:     actionID,
		ActionType:   ActionRestoration,
		Rule:         res.Rule,
		Payload:      string(mustJSON(res)),
		RequiredOrgs: orgs,
		Approvals:    []string{},
		Status:       StatusPending,
		CreatedAt:    now,
	}
	key, _ := ctx.GetStub().CreateCompositeKey(keyProposalPrefix, []string{caseID, actionID})
	b, _ := json.Marshal(prop)
	if err := ctx.GetStub().PutState(key, b); err != nil {
		return nil, err
	}
	return &prop, nil
}

// ---------- 7. ApproveAction ----------

// ApproveAction records one org's approval. When a majority of the required
// orgs have approved, the proposal becomes APPROVED. Execution of the real
// money movement stays off-chain, with each bank acting on its own accounts.
func (s *SmartContract) ApproveAction(ctx contractapi.TransactionContextInterface,
	caseID, actionID string) (*ActionProposal, error) {

	key, _ := ctx.GetStub().CreateCompositeKey(keyProposalPrefix, []string{caseID, actionID})
	b, err := ctx.GetStub().GetState(key)
	if err != nil || b == nil {
		return nil, fmt.Errorf("proposal %s not found for case %s", actionID, caseID)
	}
	var p ActionProposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.Status == StatusApproved {
		return &p, nil
	}
	msp, err := clientMSP(ctx)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, o := range p.RequiredOrgs {
		if o == msp {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("org %s is not a required approver for %s", msp, actionID)
	}
	for _, a := range p.Approvals {
		if a == msp {
			return &p, nil // already approved; idempotent
		}
	}
	p.Approvals = append(p.Approvals, msp)
	if len(p.Approvals) >= majority(len(p.RequiredOrgs)) {
		p.Status = StatusApproved
		_ = s.setStatus(ctx, caseID, StatusRestorationApproved)
	}
	out, _ := json.Marshal(p)
	if err := ctx.GetStub().PutState(key, out); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetProposal returns one proposal (read-only).
func (s *SmartContract) GetProposal(ctx contractapi.TransactionContextInterface, caseID, actionID string) (*ActionProposal, error) {
	key, _ := ctx.GetStub().CreateCompositeKey(keyProposalPrefix, []string{caseID, actionID})
	b, err := ctx.GetStub().GetState(key)
	if err != nil || b == nil {
		return nil, fmt.Errorf("proposal not found")
	}
	var p ActionProposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetCase returns one case (read-only).
func (s *SmartContract) GetCase(ctx contractapi.TransactionContextInterface, caseID string) (*FraudCase, error) {
	b, err := ctx.GetStub().GetState(keyCasePrefix + caseID)
	if err != nil || b == nil {
		return nil, fmt.Errorf("case %s not found", caseID)
	}
	var fc FraudCase
	if err := json.Unmarshal(b, &fc); err != nil {
		return nil, err
	}
	return &fc, nil
}

// ---------- misc ----------

// leaMSP is the law-enforcement org on the channel. Keep in sync with the
// network config; surfaced here so approval logic has one source of truth.
const leaMSP = "Org4MSP"

func majority(n int) int { return n/2 + 1 }

func nowUnix(rfc3339 string) int64 {
	t, _ := parseTime(rfc3339)
	return t.Unix()
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	cc, err := contractapi.NewChaincode(&SmartContract{})
	if err != nil {
		panic(err.Error())
	}
	if err := cc.Start(); err != nil {
		panic(err.Error())
	}
}
