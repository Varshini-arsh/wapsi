package main

import "time"

// FraudCase is the root record for one fraud complaint.
// All amounts are in paise (1 INR = 100 paise) to avoid float math on the ledger.
type FraudCase struct {
	DocType       string `json:"docType"` // "fraudcase"
	CaseID        string `json:"caseId"`
	ComplaintRef  string `json:"complaintRef"` // NCRP / 1930 reference
	VictimBank    string `json:"victimBank"`   // MSP ID of the bank where the victim holds the account
	ClaimedAmount int64  `json:"claimedAmount"`
	ReportedAt    string `json:"reportedAt"` // RFC3339
	Status        string `json:"status"`     // OPEN | TRACED | LIENS_RECORDED | RESTORATION_APPROVED
	CreatedBy     string `json:"createdBy"`  // client MSP ID
}

// TransactionHop is one attested transfer leg between two accounts.
// Only the source bank may attest a hop (endorsement policy enforces it; we double-check in code).
type TransactionHop struct {
	DocType      string `json:"docType"` // "hop"
	CaseID       string `json:"caseId"`
	HopID        string `json:"hopId"`
	SrcBank      string `json:"srcBank"`
	SrcAcctToken string `json:"srcAcctToken"` // HMAC token, not a real account number
	DstBank      string `json:"dstBank"`
	DstAcctToken string `json:"dstAcctToken"`
	Amount       int64  `json:"amount"`
	Timestamp    string `json:"timestamp"` // transfer time, RFC3339
	PaymentRef   string `json:"paymentRef"`
	AttestedBy   string `json:"attestedBy"` // MSP ID of the org that signed this hop
	AttestedAt   string `json:"attestedAt"` // ledger write time, RFC3339
}

// Lien is a hold placed on the traced portion of an account for a case.
type Lien struct {
	DocType     string `json:"docType"` // "lien"
	CaseID      string `json:"caseId"`
	LienID      string `json:"lienId"`
	Bank        string `json:"bank"`
	AcctToken   string `json:"acctToken"`
	LienAmount  int64  `json:"lienAmount"`
	RecordedAt  string `json:"recordedAt"`
	Deadline    string `json:"deadline"` // 90 days from RecordedAt (MHA SOP)
	Status      string `json:"status"`   // ACTIVE | RELEASE_ELIGIBLE | EXTENDED
	RecordedBy  string `json:"recordedBy"`
}

// ActionProposal is a restoration/refund/hold/release proposal awaiting approvals.
type ActionProposal struct {
	DocType       string   `json:"docType"` // "proposal"
	CaseID        string   `json:"caseId"`
	ActionID      string   `json:"actionId"`
	ActionType    string   `json:"actionType"` // RESTORATION | HOLD | RELEASE
	Rule          string   `json:"rule"`       // allocation rule used for RESTORATION
	Payload       string   `json:"payload"`    // JSON of trace output / lien details
	RequiredOrgs  []string `json:"requiredOrgs"`
	Approvals     []string `json:"approvals"` // MSP IDs that approved
	Status        string   `json:"status"`    // PENDING | APPROVED | REJECTED
	CreatedAt     string   `json:"createdAt"`
}

// TraceResult is the output of ComputeTrace, also embedded in restoration proposals.
type TraceResult struct {
	Rule             string                  `json:"rule"`
	TotalClaimed     int64                   `json:"totalClaimed"`
	TotalVerified    int64                   `json:"totalVerified"`
	TotalRecoverable int64                   `json:"totalRecoverable"`
	TotalCashedOut   int64                   `json:"totalCashedOut"`
	Accounts         []AccountTrace          `json:"accounts"`
	Victims          []VictimRecovery        `json:"victims"`
	Notes            []string                `json:"notes"`
}

// AccountTrace summarises how much fraud money an account received and holds.
type AccountTrace struct {
	Bank      string `json:"bank"`
	AcctToken string `json:"acctToken"`
	Received  int64  `json:"received"` // fraud money credited
	OwnFunds  int64  `json:"ownFunds"`
	Debited   int64  `json:"debited"` // spent after crediting, allocated under the rule
	Remaining int64  `json:"remaining"`
	Lienable  int64  `json:"lienable"`
}

// VictimRecovery tells one victim how much of their money is recoverable.
type VictimRecovery struct {
	VictimRef   string `json:"victimRef"` // token of the victim's account
	Bank        string `json:"bank"`
	Claimed     int64  `json:"claimed"`
	Verified    int64  `json:"verified"`    // money that stayed inside attested hops
	Recoverable int64  `json:"recoverable"` // per the allocation rule
	CashedOut   int64  `json:"cashedOut"`   // left the attested trail (recovery needs off-chain action)
}

// Ledger value keys.
const (
	keyCasePrefix     = "case"
	keyHopPrefix      = "hop"
	keyLienPrefix     = "lien"
	keyProposalPrefix = "proposal"
	keyHopsCasePrefix = "hopsCase" // index: hopsByCase -> {caseId} -> {hopId} -> ""
	keyLiensCasePrefix = "liensCase"
)

// Status constants.
const (
	StatusOpen                 = "OPEN"
	StatusTraced               = "TRACED"
	StatusLiensRecorded        = "LIENS_RECORDED"
	StatusRestorationApproved  = "RESTORATION_APPROVED"

	StatusLienActive          = "ACTIVE"
	StatusLienReleaseEligible = "RELEASE_ELIGIBLE"
	StatusLienExtended        = "EXTENDED"

	StatusPending  = "PENDING"
	StatusApproved = "APPROVED"

	ActionRestoration = "RESTORATION"
	ActionHold        = "HOLD"
	ActionRelease     = "RELEASE"
)

// Allocation rules.
const (
	RuleLIBR     = "LIBR"
	RuleOwnFirst = "OWN_FIRST"
	RuleFIFO     = "FIFO"
	RuleProRata  = "PRORATA"
)

var validRules = map[string]bool{
	RuleLIBR: true, RuleOwnFirst: true, RuleFIFO: true, RuleProRata: true,
}

// lienDeadlineDays implements the MHA SOP (Jan 2026): holds under Rs 50,000
// are released within 90 days unless a court extends them.
const lienDeadlineDays = 90

// ledgerTimeFormat is the time format used for all stored timestamps.
const ledgerTimeFormat = time.RFC3339
