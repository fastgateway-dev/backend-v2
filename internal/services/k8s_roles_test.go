package services

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// k8sRolesFile is the single source of truth for the role interfaces that
// replaced KubernetesServiceInterface in Phase 2E Task 7.
const k8sRolesFile = "k8s_roles.go"

// k8sRole is one role interface as declared in k8sRolesFile.
type k8sRole struct {
	Name string
	// Methods counts the role's full method set, following embedded roles
	// (PolicyApplier embeds TrafficPolicyApplier, Secrets embeds
	// SecretWriter, and so on) and counting a method promoted through two
	// embeddings only once.
	Methods int
}

// parseK8sRoles derives the role list from k8s_roles.go itself, in the
// file-parsing style TestInterfaces_NoConcreteServiceTypes already uses.
//
// It used to be a hand-maintained map[string]reflect.Type, which meant a
// twentieth role added to k8s_roles.go and forgotten here escaped the
// twelve-method cap silently: the cap guard only guarded the roles someone
// had remembered to list. Reading the declarations makes forgetting
// impossible.
func parseK8sRoles(t *testing.T) []k8sRole {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, k8sRolesFile, nil, 0)
	require.NoError(t, err)

	decls := map[string]*ast.InterfaceType{}
	var order []string
	for _, d := range file.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			iface, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			decls[ts.Name.Name] = iface
			order = append(order, ts.Name.Name)
		}
	}
	require.NotEmpty(t, decls, "no interface declarations found in %s", k8sRolesFile)

	// collect walks a role's method set, following embeddings. An embedded
	// name that is not declared in this file means a role reaching outside
	// the roles set; it is reported rather than silently counted as zero.
	var collect func(name string, into map[string]bool, seen map[string]bool) []string
	collect = func(name string, into map[string]bool, seen map[string]bool) []string {
		if seen[name] {
			return nil
		}
		seen[name] = true
		iface, ok := decls[name]
		if !ok {
			return []string{name}
		}
		var foreign []string
		for _, field := range iface.Methods.List {
			if len(field.Names) > 0 {
				for _, n := range field.Names {
					into[n.Name] = true
				}
				continue
			}
			embedded, ok := field.Type.(*ast.Ident)
			if !ok {
				foreign = append(foreign, fmt.Sprintf("%s (non-local embedded type)", name))
				continue
			}
			foreign = append(foreign, collect(embedded.Name, into, seen)...)
		}
		return foreign
	}

	roles := make([]k8sRole, 0, len(order))
	for _, name := range order {
		methods := map[string]bool{}
		foreign := collect(name, methods, map[string]bool{})
		require.Emptyf(t, foreign,
			"%s embeds a type not declared in %s: %s", name, k8sRolesFile, strings.Join(foreign, ", "))
		roles = append(roles, k8sRole{Name: name, Methods: len(methods)})
	}
	return roles
}

func TestK8sRoles_StaySmall(t *testing.T) {
	// A role interface that grows past 12 methods has stopped being a role
	// and is drifting back towards the 58-method union this split removed.
	roles := parseK8sRoles(t)
	require.NotEmpty(t, roles)
	for _, role := range roles {
		assert.LessOrEqualf(t, role.Methods, 12,
			"%s has %d methods; roles are capped at 12", role.Name, role.Methods)
	}
}

func TestK8sRoles_NoRoleIsEmpty(t *testing.T) {
	// A zero-method role would be satisfied by anything, which makes the
	// declaration meaningless -- the shape ApprovalService's deleted
	// K8sService field had in practice, since it called nothing.
	for _, role := range parseK8sRoles(t) {
		assert.Positivef(t, role.Methods, "%s declares no methods", role.Name)
	}
}

// TestK8sRoles_EveryRoleIsAsserted is the other half of deriving the list
// from source: every role declared in k8s_roles.go must also carry a
// compile-time `var _ Role = (*cluster.Client)(nil)` assertion in that same
// file, so a role the cluster client cannot satisfy fails the build rather
// than a test. A new role nobody remembered to assert is caught here.
func TestK8sRoles_EveryRoleIsAsserted(t *testing.T) {
	src, err := os.ReadFile(k8sRolesFile)
	require.NoError(t, err)
	body := string(src)

	var missing []string
	for _, role := range parseK8sRoles(t) {
		assertion := regexp.MustCompile(`_\s+` + regexp.QuoteMeta(role.Name) + `\s+=\s+\(\*cluster\.Client\)\(nil\)`)
		if !assertion.MatchString(body) {
			missing = append(missing, role.Name)
		}
	}
	assert.Emptyf(t, missing,
		"roles declared in %s with no *cluster.Client compile-time assertion:\n%s",
		k8sRolesFile, strings.Join(missing, "\n"))
}

// TestInterfaces_NoConcreteServiceTypes guards Phase 2E's other half: every
// concrete *XService that appeared in an interface signature was a setter
// parameter, and none should remain.
func TestInterfaces_NoConcreteServiceTypes(t *testing.T) {
	src, err := os.ReadFile("interfaces.go")
	require.NoError(t, err)
	re := regexp.MustCompile(`\*[A-Z][A-Za-z]*Service\b`)
	var offenders []string
	for i, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, "var _") {
			continue // compile-time assertions legitimately name concrete types
		}
		if re.MatchString(line) {
			offenders = append(offenders, fmt.Sprintf("interfaces.go:%d: %s", i+1, strings.TrimSpace(line)))
		}
	}
	assert.Emptyf(t, offenders, "concrete service types in interface signatures:\n%s",
		strings.Join(offenders, "\n"))
}

// TestInterfaces_NoSetters is the companion guard: an interface that declares
// a Set* method is asking its implementations to be mutable after
// construction, which is what Phase 2E removed.
func TestInterfaces_NoSetters(t *testing.T) {
	src, err := os.ReadFile("interfaces.go")
	require.NoError(t, err)
	// SetAllowedMethods is a business operation on ClientService, not a
	// dependency setter: it takes (uuid.UUID, []string) and returns
	// (*models.Client, error). Task 1 classified it NOT-A-SETTER.
	re := regexp.MustCompile(`^\tSet[A-Z][A-Za-z]*\(`)
	var offenders []string
	for i, line := range strings.Split(string(src), "\n") {
		if !re.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "SetAllowedMethods(") {
			continue
		}
		offenders = append(offenders, fmt.Sprintf("interfaces.go:%d: %s", i+1, strings.TrimSpace(line)))
	}
	assert.Emptyf(t, offenders, "dependency setters still declared on interfaces:\n%s",
		strings.Join(offenders, "\n"))
}

// TestNoWiringNilGuardsRemain is Phase 2E Task 9's regression guard. Every
// dependency is a required constructor parameter now, so a nil-guard on one
// is unreachable: the constructor panics at start-up long before the method
// runs. Only genuinely optional dependencies and nilable *values* may be
// guarded.
//
// The pattern deliberately allows a digit in the field name. Task 1's
// enumeration command used [a-zA-Z]* and therefore never saw
// route_deploy.go's compound k8s* guard -- the one controller ruling R10 had
// to name by hand.
//
// The allowlist is keyed on file:field, matching
// TestNoWiringNilGuardsRemain_NegatedForm below. It used to be keyed on file
// alone, which skipped the whole file: a NEW `== nil` guard on any other
// field of ai_service.go or route_service_idgen.go escaped the check
// entirely.
func TestNoWiringNilGuardsRemain(t *testing.T) {
	allowed := map[string]string{
		// The Phase 2A determinism seam: nil means uuid.New. Deleting it
		// breaks preview reproducibility, because the first 8 hex characters
		// of the ID minted in PreviewCreate appear in every previewed
		// resource name.
		"route_service_idgen.go:idgen": "idgen determinism seam (Phase 2A)",
		// s.config is a nilable *value* -- a plain *config.Config passed to
		// NewAIService, never a wired dependency. IsEnabled reports "no AI
		// provider configured" through it.
		"ai_service.go:config": "config is a nilable value, not an injected dependency",
	}

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	re := regexp.MustCompile(`if s\.([a-zA-Z0-9]+) == nil`)

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for i, line := range strings.Split(string(src), "\n") {
			// Strip comments, as the negated-form test does: a file that
			// DESCRIBES a deleted guard in prose is not a guard.
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			for _, m := range re.FindAllStringSubmatch(code, -1) {
				if _, ok := allowed[name+":"+m[1]]; ok {
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", name, i+1, strings.TrimSpace(line)))
			}
		}
	}
	assert.Emptyf(t, offenders, "wiring nil-guards remain:\n%s", strings.Join(offenders, "\n"))
}

// TestNoWiringNilGuardsRemain_NegatedForm covers the OTHER half of the guard
// class, which had no regression test at all.
//
// TestNoWiringNilGuardsRemain above pins only `if s.<dep> == nil` -- the
// bail-out shape. Tasks 9 and 10 deleted 64 guards of the POSITIVE shape,
// `if s.<dep> != nil { ...do the work... }`, plus 14 more as conjuncts
// (`... && s.<field> != nil`). Those are the more dangerous of the two: a
// `== nil` guard that stops working turns into a nil dereference and is
// obvious, while a `!= nil` guard that stops working silently SKIPS the work
// -- which is exactly the degraded-path class this phase set out to remove
// (a Kubernetes resource never applied, a cascade never fanned out, an
// approval side effect never run, and no error anywhere).
//
// Nothing prevented that shape from creeping back. This test does. One
// regexp catches both forms, since the conjunct is a substring of the same
// expression.
//
// The allowlist is keyed on file:field, not on file, so a NEW `!= nil` guard
// on a different field in an allowlisted file still fails.
func TestNoWiringNilGuardsRemain_NegatedForm(t *testing.T) {
	// The four guards deliberately kept. Each is a genuinely optional
	// dependency or a cache slot, NOT wiring.
	allowed := map[string]string{
		// A memoised provider, rebuilt when the config key changes. nil
		// means "not built yet", not "not wired".
		"ai_service.go:provider": "provider cache slot; nil means not yet constructed for this config key",

		// A memoised settings row, populated on first read.
		"system_settings_service.go:cached": "settings cache slot; nil means not yet loaded",

		// DomainTemplateService.aiService is genuinely optional. Its
		// constructor is still positional (NewDomainTemplateService(dtRepo,
		// projectRepo, domainRepo, k8sService, aiService)) with no nil checks,
		// and all 20 test call sites pass nil for it, so the AI review is
		// skipped rather than the service refusing to construct. This is the
		// last service in the package with an unchecked positional
		// constructor; converting it would retire these two entries. Recorded
		// as known deviation (d) in verification.md.
		"domain_template_service.go:aiService": "genuinely-optional AI review dependency; see known deviation (d)",
	}

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	re := regexp.MustCompile(`s\.([a-zA-Z0-9]+) != nil`)

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for i, line := range strings.Split(string(src), "\n") {
			// Strip comments: several files DESCRIBE a deleted guard in prose
			// (domain_service.go and project_service.go both quote the exact
			// expression they no longer contain), and a description is not a
			// guard.
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			for _, m := range re.FindAllStringSubmatch(code, -1) {
				if _, ok := allowed[name+":"+m[1]]; ok {
					continue
				}
				offenders = append(offenders,
					fmt.Sprintf("%s:%d: %s", name, i+1, strings.TrimSpace(line)))
			}
		}
	}
	assert.Emptyf(t, offenders,
		"negated-form wiring nil-guards remain. Every dependency is a required\n"+
			"constructor parameter, so `if s.x != nil { ... }` silently skips work\n"+
			"that must always run. Add to the allowlist only if the dependency is\n"+
			"genuinely optional, and say why:\n%s", strings.Join(offenders, "\n"))
}

// The allowlist above must stay a list of REAL guards. An entry that no longer
// matches anything is a stale exemption: it would silently permit the shape to
// return at that file:field the moment someone reintroduces it.
func TestNoWiringNilGuardsRemain_NegatedForm_AllowlistIsExact(t *testing.T) {
	expected := map[string]int{
		"ai_service.go:provider":               1,
		"system_settings_service.go:cached":    1,
		"domain_template_service.go:aiService": 2,
	}

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	re := regexp.MustCompile(`s\.([a-zA-Z0-9]+) != nil`)

	found := map[string]int{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for _, line := range strings.Split(string(src), "\n") {
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			for _, m := range re.FindAllStringSubmatch(code, -1) {
				found[name+":"+m[1]]++
			}
		}
	}

	assert.Equal(t, expected, found,
		"the four kept `!= nil` guards changed. If one was removed, drop its\n"+
			"allowlist entry in TestNoWiringNilGuardsRemain_NegatedForm too;\n"+
			"if one was added, justify it there first.")
}
