package dispatch

// Ticket is a Beads issue as the loop sees it: status, priority, labels and what it depends on.
type Ticket struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	IssueType    string   `json:"issue_type"` // task, bug, feature, epic, …
	Priority     *int     `json:"priority"`
	Labels       []string `json:"labels"`
	Dependencies []Ticket `json:"dependencies"` // from bd show; each carries its status and labels
	// DependencyType is how a dependency links to the ticket (blocks, related, parent-child,
	// discovered-from); set only on the entries of Dependencies.
	DependencyType string `json:"dependency_type"`
	// DependencyCount is how many tickets block this one, closed or not, as bd ready counts them
	// (bd show counts every link); nil when bd doesn't say. It changes when a blocks link is added
	// or removed.
	DependencyCount *int `json:"dependency_count"`
}

// HumanLabel marks a question for the maintainer (bd human list / respond). Workers ask one as
// its own ticket that blocks theirs; the orchestrator never dispatches it.
const HumanLabel = "human"

// HasLabel reports whether the ticket carries the label.
func HasLabel(t Ticket, label string) bool {
	for _, l := range t.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// OpenQuestion returns the unanswered question the ticket waits on, if any. Only a blocks link
// counts: a related or parent-child link to a question doesn't hold the ticket out of bd ready.
func OpenQuestion(t Ticket) *Ticket {
	for i, d := range t.Dependencies {
		if d.DependencyType == "blocks" && d.Status != "closed" && HasLabel(d, HumanLabel) {
			return &t.Dependencies[i]
		}
	}
	return nil
}

// UnmergedLabel marks a ticket closed but left unmerged (MERGE_CONFLICT, CHECKS_FAILED, …), so later
// runs still hold the tickets it blocks: bd ready counts a closed blocker as done. The loop removes
// it once the ticket merges.
const UnmergedLabel = "unmerged"
