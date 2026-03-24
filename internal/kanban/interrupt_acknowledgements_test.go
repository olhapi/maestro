package kanban

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestInterruptAcknowledgementsPersistAcrossStoreRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := store.AcknowledgeInterrupt("alert-1"); err != nil {
		t.Fatalf("AcknowledgeInterrupt: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	acknowledged, err := reopened.InterruptAcknowledged("alert-1")
	if err != nil {
		t.Fatalf("InterruptAcknowledged: %v", err)
	}
	if !acknowledged {
		t.Fatal("expected acknowledgement to survive reopen")
	}

	acknowledgements, err := reopened.ListInterruptAcknowledgements([]string{"alert-1", "alert-1", "alert-2"})
	if err != nil {
		t.Fatalf("ListInterruptAcknowledgements: %v", err)
	}
	if len(acknowledgements) != 1 {
		t.Fatalf("expected one acknowledgement result, got %+v", acknowledgements)
	}
	if _, ok := acknowledgements["alert-1"]; !ok {
		t.Fatalf("expected alert-1 acknowledgement in %+v", acknowledgements)
	}
}

func TestInterruptAcknowledgementsValidateRequiredID(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.AcknowledgeInterrupt(" "); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if _, err := store.InterruptAcknowledged(""); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestPruneInterruptAcknowledgementsKeepsOnlyMatchingActiveIDs(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, interruptID := range []string{"alert-1", "alert-2", "other-1"} {
		if err := store.AcknowledgeInterrupt(interruptID); err != nil {
			t.Fatalf("AcknowledgeInterrupt(%q): %v", interruptID, err)
		}
	}

	if err := store.PruneInterruptAcknowledgements("alert-", []string{"alert-2"}); err != nil {
		t.Fatalf("PruneInterruptAcknowledgements: %v", err)
	}

	if acknowledged, err := store.InterruptAcknowledged("alert-1"); err != nil {
		t.Fatalf("InterruptAcknowledged(alert-1): %v", err)
	} else if acknowledged {
		t.Fatal("expected alert-1 acknowledgement to be pruned")
	}
	if acknowledged, err := store.InterruptAcknowledged("alert-2"); err != nil {
		t.Fatalf("InterruptAcknowledged(alert-2): %v", err)
	} else if !acknowledged {
		t.Fatal("expected alert-2 acknowledgement to be retained")
	}
	if acknowledged, err := store.InterruptAcknowledged("other-1"); err != nil {
		t.Fatalf("InterruptAcknowledged(other-1): %v", err)
	} else if !acknowledged {
		t.Fatal("expected non-matching acknowledgement to be retained")
	}
}
