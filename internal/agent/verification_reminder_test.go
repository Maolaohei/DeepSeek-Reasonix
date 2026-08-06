package agent

import (
	"testing"

	"reasonix/internal/evidence"
)

// TestNeedsVerificationReminder covers the soft-reminder contract: ordinary
// turns that wrote files without a verification command need the reminder;
// delivery turns, read-only turns, and verified turns do not.
func TestNeedsVerificationReminder(t *testing.T) {
	newAgent := func(delivery bool) *Agent {
		return &Agent{deliveryProfile: delivery, evidence: evidence.NewLedger()}
	}

	// No writes: no reminder.
	if newAgent(false).NeedsVerificationReminder() {
		t.Fatal("read-only turn must not need a verification reminder")
	}

	// Wrote a file, no verification: reminder.
	a := newAgent(false)
	a.evidence.Record(evidence.Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true})
	if !a.NeedsVerificationReminder() {
		t.Fatal("write without verification must need the reminder")
	}

	// Delivery profile: no reminder (delivery has its own hard gate).
	d := newAgent(true)
	d.evidence.Record(evidence.Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true})
	if d.NeedsVerificationReminder() {
		t.Fatal("delivery mode must not use the soft reminder (hard gate exists)")
	}

	// Wrote + verification command: reminder clears.
	v := newAgent(false)
	v.evidence.Record(evidence.Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true})
	v.evidence.Record(evidence.Receipt{ToolName: "bash", Success: true, Command: "go test ./...", Mutation: false})
	if v.NeedsVerificationReminder() {
		t.Fatal("verification command must clear the reminder")
	}

	// remember-only mutation is not a file write for this purpose.
	r := newAgent(false)
	r.evidence.Record(evidence.Receipt{ToolName: "remember", Success: true, Mutation: true})
	if r.NeedsVerificationReminder() {
		t.Fatal("remember-only must not trigger the reminder")
	}
}
