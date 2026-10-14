package macvpn

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type transactionBackend struct {
	owned        map[string]bool
	applyFailure map[string]error
	ambiguous    map[string]bool
	undoFailure  map[string]error
	probeFailure map[string]error
	commands     []string
}

func newTransactionBackend() *transactionBackend {
	return &transactionBackend{owned: map[string]bool{}, applyFailure: map[string]error{}, ambiguous: map[string]bool{}, undoFailure: map[string]error{}, probeFailure: map[string]error{}}
}
func (e *transactionBackend) execute(_ context.Context, c command) ([]byte, error) {
	e.commands = append(e.commands, c.Name)
	if c.Name[:4] == "add:" {
		name := c.Name[4:]
		if e.applyFailure[name] != nil {
			if e.ambiguous[name] {
				e.owned[name] = true
			}
			return nil, e.applyFailure[name]
		}
		e.owned[name] = true
		return nil, nil
	}
	name := c.Name[7:]
	if err := e.undoFailure[name]; err != nil {
		return nil, err
	}
	delete(e.owned, name)
	return nil, nil
}
func (e *transactionBackend) inspect(_ context.Context, s step) (bool, error) {
	name := s.Apply.Name[4:]
	return e.owned[name], e.probeFailure[name]
}
func transactionSteps(names ...string) []step {
	var steps []step
	for _, name := range names {
		steps = append(steps, step{Apply: command{Name: "add:" + name}, Undo: command{Name: "remove:" + name}})
	}
	return steps
}
func applyJournal(journal *[]step) func(int, step) error {
	return func(i int, s step) error {
		if i == len(*journal) {
			*journal = append(*journal, s)
		} else {
			(*journal)[i] = s
		}
		return nil
	}
}
func cleanupJournal(journal *[]step) func([]step) error {
	return func(remaining []step) error { *journal = append([]step(nil), remaining...); return nil }
}

func TestTransactionJournalsIntentAndCommitSeparately(t *testing.T) {
	e := newTransactionBackend()
	var snapshots [][]step
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("route"), func(i int, s step) error {
		if err := applyJournal(&journal)(i, s); err != nil {
			return err
		}
		snapshots = append(snapshots, append([]step(nil), journal...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || snapshots[0][0].Applied || !snapshots[1][0].Applied {
		t.Fatal("mutation intent and confirmed success were not both journaled")
	}
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 || e.owned["route"] {
		t.Fatal("successful cleanup retained resource or journal")
	}
}

func TestFailedAddNeverRemovesForeignRoute(t *testing.T) {
	e := newTransactionBackend()
	e.applyFailure["route"] = errors.New("route already exists")
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("route"), applyJournal(&journal)); err == nil {
		t.Fatal("fixture add unexpectedly succeeded")
	}
	if len(journal) != 1 || journal[0].Applied {
		t.Fatal("failed add claimed ownership")
	}
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.commands, []string{"add:route"}) {
		t.Fatalf("foreign route was deleted: %v", e.commands)
	}
}

func TestTimedOutAddCleansOnlyConfirmedOwnedResource(t *testing.T) {
	e := newTransactionBackend()
	e.applyFailure["route"] = context.DeadlineExceeded
	e.ambiguous["route"] = true
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("route"), applyJournal(&journal)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("fixture timeout missing")
	}
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if e.owned["route"] || len(journal) != 0 {
		t.Fatal("successful but timed-out owned route was abandoned")
	}
}

func TestReplacedResourceIsPreservedAfterSuccessfulAdd(t *testing.T) {
	e := newTransactionBackend()
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("route"), applyJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	// The platform inspector identifies a different ownership marker after an
	// external process replaces the exact destination/gateway route.
	e.owned["route"] = false
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if len(e.commands) != 1 {
		t.Fatal("replacement was deleted solely because the original add succeeded")
	}
}

func TestCleanupAttemptsAllResourcesAndRetainsOnlyFailures(t *testing.T) {
	e := newTransactionBackend()
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("underlay", "capture", "dns"), applyJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	e.undoFailure["capture"] = errors.New("route delete temporarily failed")
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err == nil {
		t.Fatal("failed cleanup reported success")
	}
	if !reflect.DeepEqual(e.commands, []string{"add:underlay", "add:capture", "add:dns", "remove:dns", "remove:capture", "remove:underlay"}) {
		t.Fatalf("cleanup stopped early: %v", e.commands)
	}
	if len(journal) != 1 || journal[0].Apply.Name != "add:capture" || e.owned["dns"] || e.owned["underlay"] {
		t.Fatal("cleanup did not preserve exactly the failed resource")
	}
	delete(e.undoFailure, "capture")
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 || e.owned["capture"] {
		t.Fatal("cleanup retry failed")
	}
}

func TestProbeFailuresAreNotTreatedAsAbsence(t *testing.T) {
	e := newTransactionBackend()
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("route", "dns"), applyJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	e.probeFailure["route"] = context.DeadlineExceeded
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("failed probe was discarded")
	}
	if len(journal) != 1 || !e.owned["route"] || e.owned["dns"] {
		t.Fatal("probe failure erased cleanup intent or blocked other cleanup")
	}
}

func TestJournalWriteFailurePreventsApplyAndRetainsCleanupIntent(t *testing.T) {
	e := newTransactionBackend()
	writeError := errors.New("journal storage unavailable")
	if err := apply(context.Background(), e, transactionSteps("route"), func(int, step) error { return writeError }); !errors.Is(err, writeError) {
		t.Fatal("intent persistence failure missing")
	}
	if len(e.commands) != 0 {
		t.Fatal("resource changed without durable cleanup intent")
	}
	var journal []step
	if err := apply(context.Background(), e, transactionSteps("route"), applyJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if err := rollback(context.Background(), e, journal, func([]step) error { return writeError }); !errors.Is(err, writeError) {
		t.Fatal("cleanup persistence failure missing")
	}
	if len(journal) != 1 || e.owned["route"] {
		t.Fatal("unpersisted cleanup did not retain a retryable original journal")
	}
	if err := rollback(context.Background(), e, journal, cleanupJournal(&journal)); err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 {
		t.Fatal("positive absence could not finish journal cleanup")
	}
}

func TestCommitWriteFailureCanRecoverFromDurableIntent(t *testing.T) {
	e := newTransactionBackend()
	var durableJournal []step
	writeError := errors.New("confirmed success could not be persisted")
	err := apply(context.Background(), e, transactionSteps("route"), func(i int, s step) error {
		if s.Applied {
			return writeError
		}
		return applyJournal(&durableJournal)(i, s)
	})
	if !errors.Is(err, writeError) || len(durableJournal) != 1 || durableJournal[0].Applied || !e.owned["route"] {
		t.Fatal("fixture did not preserve intent for an uncommitted successful add")
	}
	if err := rollback(context.Background(), e, durableJournal, cleanupJournal(&durableJournal)); err != nil {
		t.Fatal(err)
	}
	if len(durableJournal) != 0 || e.owned["route"] {
		t.Fatal("restart from durable intent could not clean the owned resource")
	}
}
