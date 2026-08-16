package rename

import (
	"fmt"
	"os"
	"path/filepath"
)

// Apply executes a plan on disk: every file move first (inside the old dir),
// then the directory itself. Returns the moves actually performed; a failed
// step aborts and rolls back what it can (best effort - renames are not
// transactional).
func Apply(plan Plan, dirAbsOld string) error {
	if plan.DirNew == plan.DirOld {
		// file-only rename (dir name already canonical)
		for _, mv := range plan.Moves {
			if err := os.Rename(mv.From, mv.To); err != nil {
				return fmt.Errorf("rename %s: %w", mv.From, err)
			}
		}
		return nil
	}
	dirAbsNew := filepath.Join(filepath.Dir(dirAbsOld), plan.DirNew)

	done := make([]string, 0, len(plan.Moves))

	// 1. rename files inside the dir (From/To both live in dirAbsOld)
	for _, mv := range plan.Moves {
		if err := os.Rename(mv.From, mv.To); err != nil {
			// roll back earlier file renames
			for i := len(done) - 1; i >= 0; i-- {
				_ = os.Rename(done[i], plan.Moves[i].From)
			}
			return fmt.Errorf("rename %s: %w", mv.From, err)
		}
		done = append(done, mv.To)
	}
	// 2. rename the directory itself
	if err := os.Rename(dirAbsOld, dirAbsNew); err != nil {
		// roll back file renames, leave the dir in place
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.Rename(done[i], plan.Moves[i].From)
		}
		return fmt.Errorf("rename dir %s: %w", dirAbsOld, err)
	}
	return nil
}
