package dispatch

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// What the organs cost: each call claude answers is logged with its cost and turns (an ORGAN line),
// and the run keeps the total, which the reviewer is given and the run report ends with.

// OrganSpent logs what an organ call cost: "ORGAN triage: $0.0123 in 1 turn, 4.2s, session …".
func (l *Log) OrganSpent(s organ.Spend) {
	l.Line(time.Now(), "ORGAN "+s.String())
}

// organCost is what the run's organ calls cost, as their results said.
type organCost struct {
	mu    sync.Mutex
	calls map[string]int // per organ
	usd   float64
	turns int
}

func (c *organCost) add(s organ.Spend) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[s.Organ]++
	c.usd += s.CostUSD
	c.turns += s.Turns
}

// summary reads "$0.0456 in 3 organ calls (predict 2, triage 1), 3 turns in all", or "" before the
// first call.
func (c *organCost) summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	var each []string
	for _, name := range slices.Sorted(maps.Keys(c.calls)) {
		n += c.calls[name]
		each = append(each, fmt.Sprintf("%s %d", name, c.calls[name]))
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("$%.4f in %s (%s), %s in all", c.usd, counted(n, "organ call"), strings.Join(each, ", "),
		counted(c.turns, "turn"))
}

// counted reads "1 turn" or "3 turns".
func counted(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// organs is the loop's organ client, which logs what each call cost and adds it to the run's.
func (o *Loop) organs() organ.Client {
	g := o.organ
	before := g.Spent
	g.Spent = func(s organ.Spend) {
		if before != nil {
			before(s)
		}
		o.log.OrganSpent(s)
		o.organCost.add(s)
	}
	return g
}

// organCostEvidence is the reviewer's line on what the run's organs cost before the report.
func (o *Loop) organCostEvidence() string {
	if s := o.organCost.summary(); s != "" {
		return s + ", before this report."
	}
	return "No organ call before this report said what it cost."
}

// organCostLine ends the run report with what the run's organs cost, the report's own call included;
// "" when no call said.
func (o *Loop) organCostLine() string {
	if s := o.organCost.summary(); s != "" {
		return "\nOrgans: " + s + ".\n"
	}
	return ""
}
