package approval

import (
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// Engine owns approval stage planning and traversal for every approvable
// entity type.
type Engine struct {
	approvals  ApprovalStore
	stages     StageReviewStore
	policies   PolicyStore
	teams      TeamLookup
	projects   ProjectLookup
	completers map[models.ApprovalEntityType]Completer
}

// New builds an Engine. Every dependency is required and a nil one PANICS,
// naming the offender.
//
// Nothing here is optional, because every pre-2D nil-guard on one of these
// was a gate that silently widened when the dependency happened to be
// unwired: a nil policy store fell back to a single default stage in place
// of a project's real multi-stage policy, a nil stage-review store
// downgraded MinApprovers>1 to 1, and a nil project store let a submitter
// approve their own submission. Missing wiring must fail at construction,
// not degrade silently at runtime.
//
// It panics rather than returning an error because the only caller is
// process start-up (cmd/server/main.go wires every service in a straight
// line of constructors, none of which returns an error), and because a
// panic keeps New's signature the single value the rest of the package and
// Tasks 7-8 are written against. A nil dependency is a programming error in
// the wiring, not a runtime condition worth handling.
func New(
	approvals ApprovalStore,
	stages StageReviewStore,
	policies PolicyStore,
	teams TeamLookup,
	projects ProjectLookup,
) *Engine {
	var missing []string
	if approvals == nil {
		missing = append(missing, "approvals ApprovalStore")
	}
	if stages == nil {
		missing = append(missing, "stages StageReviewStore")
	}
	if policies == nil {
		missing = append(missing, "policies PolicyStore")
	}
	if teams == nil {
		missing = append(missing, "teams TeamLookup")
	}
	if projects == nil {
		missing = append(missing, "projects ProjectLookup")
	}
	if len(missing) > 0 {
		panic("approval.New: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &Engine{
		approvals:  approvals,
		stages:     stages,
		policies:   policies,
		teams:      teams,
		projects:   projects,
		completers: make(map[models.ApprovalEntityType]Completer),
	}
}

// Register associates a Completer with an entity type. The service owning
// the entity registers itself at wiring time.
//
// The nil-map check below is NOT an optional-dependency guard of the kind
// New now rejects: New always allocates the map, and an entity type with no
// completer is an error in completerFor rather than a silent no-op. It only
// keeps a zero-value Engine (as Task 5's planning tests construct) from
// panicking on a nil-map write.
func (e *Engine) Register(t models.ApprovalEntityType, c Completer) {
	if e.completers == nil {
		e.completers = make(map[models.ApprovalEntityType]Completer)
	}
	e.completers[t] = c
}

// completerFor returns the registered Completer for an entity type. An
// unregistered type is an error: silently skipping completion would leave
// the entity in a pending state for ever.
//
// This replaces the pre-2D `default: return nil` arms of
// ApprovalService.onApprovalComplete / onApprovalRejected /
// onApprovalCancelled and their `if s.clientAttachmentService != nil`
// guards, all of which reported success while doing nothing.
func (e *Engine) completerFor(t models.ApprovalEntityType) (Completer, error) {
	c, ok := e.completers[t]
	if !ok {
		return nil, fmt.Errorf("no completer registered for entity type %q", t)
	}
	return c, nil
}
