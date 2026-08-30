package engine

import (
	"fmt"
	"strings"

	"gclean/internal/models"
	"gclean/internal/storage"
)

// Pipeline is the scan→plan→trash seam. Previously the 6-step flow
// (open store → fetch → classify → upsert → plan → set verdict →
// trash → write undo cache) lived inline across runScan + planAndApply in
// internal/cli, duplicated by scan / dry-run / clean / undo handlers. The
// CLI now builds a Pipeline and runs a slice of stages; each stage is
// independently testable.
//
// The Pipeline holds already-resolved dependencies (store, client, config).
// It does no env/path/file discovery itself — the CLI owns that so the
// engine stays deterministic and pure (its documented invariant).
type Pipeline struct {
	Store     *storage.Store
	Reader    MessageReader
	Mutations MutationClient
	Keep      KeepConfig
	Rules     RuleConfig
	// CachePath is the undo-cache file the Apply stage writes to. Empty
	// disables caching (some callers, e.g. dry-run, don't trash).
	CachePath     string
	SelectionPath string
	// Account is the authenticated Gmail account that owns Store and any undo
	// batch created by Apply.
	Account string
	// MutationLockHeld is set by callers that lock around Plan+Apply together.
	MutationLockHeld bool
	// SelectedSenders bypasses SelectionPath when SelectionLimited is true.
	// This lets interactive clients represent an explicit empty cohort.
	SelectedSenders  map[string]struct{}
	SelectionLimited bool

	// stage-populated state, read by the CLI to render output.
	scanned        int
	decisions      []models.Decision
	report         models.DryRunReport
	trashedIDs     []string
	trashedRecords []storage.StoredMessage
}

// MessageReader is the non-mutating Gmail scan seam. Mutation commands use
// MutationClient, keeping the read and mutation adapters non-overlapping.
type MessageReader interface {
	ListMessages(query string, max int) ([]*models.Message, error)
}

// Stage is one step of the pipeline. Each stage mutates the shared Pipeline
// state and returns an error to abort. Keeping stages small is what makes
// the deletion test meaningful: "would deleting this stage concentrate
// complexity or merely move it?" — a stage concentrates.
type Stage func(p *Pipeline) error

// Run executes stages in order, stopping at the first error.
func (p *Pipeline) Run(stages ...Stage) error {
	for _, s := range stages {
		if err := s(p); err != nil {
			return err
		}
	}
	return nil
}

// ScanStages is the full ingest path: fetch + classify + persist.
// Used by `scan`. (Store open/close is owned by the CLI.)
func (p *Pipeline) ScanStages() []Stage {
	return []Stage{p.fetchAndClassify}
}

// PlanStages loads classified rows, runs the planner, and writes verdicts
// back. NO Gmail interaction. Used by `dry-run` and as the first half of
// `clean`.
func (p *Pipeline) PlanStages() []Stage {
	return []Stage{p.loadPlan}
}

// ApplyStages trashes the delete cohort and writes the undo cache. Must run
// after PlanStages. Used by `clean`.
func (p *Pipeline) ApplyStages() []Stage {
	return []Stage{p.applyTrash}
}

// fetchAndClassify pulls messages, classifies each, and upserts to SQLite.
func (p *Pipeline) fetchAndClassify(pl *Pipeline) error {
	lock, err := storage.AcquireMutationLock(pl.CachePath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	if err := pl.Store.BindAccount(pl.Account); err != nil {
		return err
	}
	msgs, err := pl.Reader.ListMessages("", 0)
	if err != nil {
		return fmt.Errorf("list messages: %w", err)
	}
	records := make([]storage.StoredMessage, 0, len(msgs))
	for _, m := range msgs {
		c := Classify(m)
		records = append(records, storage.FromClassified(&c, models.VerdictKeep))
	}
	if err := pl.Store.ReplaceAll(records); err != nil {
		return fmt.Errorf("replace scanned metadata: %w", err)
	}
	pl.scanned = len(msgs)
	return nil
}

// loadPlan runs the planner and persists verdicts. It never touches Gmail.
func (p *Pipeline) loadPlan(pl *Pipeline) error {
	classified, err := pl.Store.AllClassified()
	if err != nil {
		return err
	}
	selected := pl.SelectedSenders
	if !pl.SelectionLimited {
		selected, err = loadSelectedSenders(pl.SelectionPath)
		if err != nil {
			return err
		}
	}
	decisions, rep := Plan(PlanInputs{
		Messages:         classified,
		Config:           pl.Rules,
		Keep:             pl.Keep,
		SelectedSenders:  selected,
		SelectionLimited: pl.SelectionLimited,
	})
	for _, d := range decisions {
		reasons := strings.Join(d.Reasons, ";")
		if err := pl.Store.SetVerdict(d.Message.ID, int(d.Verdict), reasons, d.Verdict == models.VerdictProtected); err != nil {
			return fmt.Errorf("set verdict %s: %w", d.Message.ID, err)
		}
	}
	pl.decisions = decisions
	pl.report = rep
	return nil
}

// applyTrash moves the delete cohort to Trash and stashes the originals for
// undo. It is the ONLY stage that performs Gmail mutation. The mutation
// journal owns reconciliation and reports the observed subset back onto the
// pipeline state for the CLI to render.
func (p *Pipeline) applyTrash(pl *Pipeline) error {
	if err := pl.Store.BindAccount(pl.Account); err != nil {
		return err
	}
	var lock *storage.MutationLock
	if !pl.MutationLockHeld {
		var err error
		lock, err = storage.AcquireMutationLock(pl.CachePath)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Unlock() }()
	}
	ids := []string{}
	toTrash := []storage.StoredMessage{}
	for _, d := range pl.decisions {
		if d.Verdict != models.VerdictDelete {
			continue
		}
		ids = append(ids, d.Message.ID)
		toTrash = append(toTrash, storage.FromClassified(d.Classified, models.VerdictDelete))
	}
	if len(ids) == 0 {
		return nil
	}
	if pl.CachePath != "" {
		if err := storage.SaveUndoCacheForAccount(pl.CachePath, pl.Account, toTrash); err != nil {
			return fmt.Errorf("save undo cache: %w", err)
		}
	}
	journal := &Reconciler{Store: pl.Store, CachePath: pl.CachePath, Account: pl.Account, Client: pl.Mutations}
	outcome, err := journal.Apply(Intent{Mutation: MutationTrash, Records: toTrash}, func() error {
		return pl.Mutations.TrashMessages(ids)
	})
	pl.trashedIDs = outcome.Moved
	pl.trashedRecords = outcome.MovedRecords
	return err
}

// Exported accessors for the CLI to render output after a run.
func (p *Pipeline) Scanned() int                            { return p.scanned }
func (p *Pipeline) Report() models.DryRunReport             { return p.report }
func (p *Pipeline) Decisions() []models.Decision            { return p.decisions }
func (p *Pipeline) TrashedIDs() []string                    { return p.trashedIDs }
func (p *Pipeline) TrashedRecords() []storage.StoredMessage { return p.trashedRecords }

func loadSelectedSenders(path string) (map[string]struct{}, error) {
	if path == "" {
		return nil, nil
	}
	senders, err := storage.LoadSelection(path)
	if err != nil {
		return nil, fmt.Errorf("load sender selection: %w", err)
	}
	if len(senders) == 0 {
		return nil, nil
	}
	selected := make(map[string]struct{}, len(senders))
	for _, sender := range senders {
		selected[sender] = struct{}{}
	}
	return selected, nil
}
