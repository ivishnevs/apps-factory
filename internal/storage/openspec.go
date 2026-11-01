package storage

import (
	"fmt"

	"appfactory/internal/openspec"
	"appfactory/internal/tasks"
)

// OpenSpec lives in the project's repository (openspec-in-git): Core stores no OpenSpec documents. It
// validates a task's openspec_change by name only; the node checks the change on the task's checkout before
// every run (outcome change_missing), and the done facts report the change's state (openspec_changes.go).

// checkTaskChange validates the name of a task's openspec_change (create, update, ready).
func checkTaskChange(t tasks.Task) error {
	if t.OpenSpecChange != "" && !openspec.ValidChange(t.OpenSpecChange) {
		return fmt.Errorf("%w: openspec: invalid change name %q (kebab-case: lowercase letters, digits and dashes)", ErrInput, t.OpenSpecChange)
	}
	return nil
}
