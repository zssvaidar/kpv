// The rule in the code and the rule in the comment have to be the same rule.
//
// #4 shipped one of these for originRequired: a test that fails when the
// constant and the prose beside it disagree, so that a change to a security
// boundary cannot be made quietly in one of the two places. The rule adopted
// for a request with no Origin header is bigger than one constant, so this is
// the same assertion made over it: which values vouch, what happens to a
// client that sends neither header, and how the guard reads it.

package httpapi

import (
	"go/ast"
	"strings"
	"testing"
)

// docOf returns the doc comment of the named top-level declaration, or fails
// when there is none. It reads this package's own source, like source_test.go.
func docOf(t *testing.T, name string) string {
	t.Helper()
	_, files := parsePackage(t)
	for _, file := range files {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Name.Name != name {
				continue
			}
			if function.Doc == nil {
				t.Fatalf("%s has no doc comment, so the rule it implements is written down nowhere", name)
			}
			return function.Doc.Text()
		}
	}
	t.Fatalf("there is no %s in this package any more, so this test is not checking the rule", name)
	return ""
}

// The documented rule, written out as the thing to check against: these are
// the four values the Fetch standard defines for Sec-Fetch-Site, and which of
// them the comment in origin.go says are accepted.
//
// Changing the code without changing the comment fails here. Changing the
// comment without changing the code fails here. Adding a value to the switch
// without writing down why it is safe fails here.
func TestTheFetchMetadataRuleIsTheDocumentedOne(t *testing.T) {
	t.Parallel()

	documented := map[string]bool{
		"same-origin": true,
		"none":        true,
		"same-site":   false,
		"cross-site":  false,
	}

	for value, vouches := range documented {
		if got := fetchSiteVouches(value); got != vouches {
			t.Errorf("fetchSiteVouches(%q) is %v, but the documented rule in origin.go has it %v; change one or the other deliberately", value, got, vouches)
		}
	}

	doc := docOf(t, "fetchSiteVouches")
	for value, vouches := range documented {
		mentioned := strings.Contains(doc, value)
		if vouches && !mentioned {
			t.Errorf("%q is accepted by the code but not named in the rule beside it", value)
		}
		if !vouches && !mentioned {
			t.Errorf("%q is rejected by the code, and the rule beside it does not say so; a reader cannot tell this was decided rather than overlooked", value)
		}
	}
	// The behaviour for a client that sends neither header is a criterion of
	// its own, so the comment has to state it and not leave it to be inferred
	// from the absence of a sentence.
	if !strings.Contains(doc, "neither") {
		t.Error("the rule beside fetchSiteVouches does not say what happens to a client that sends neither an Origin nor a Sec-Fetch-Site")
	}
	// And the narrowing, each part of which something else inherits if it is
	// dropped.
	for _, part := range []string{"GET", "HEAD", "upgrade", "token"} {
		if !strings.Contains(doc, part) {
			t.Errorf("the rule beside fetchSiteVouches no longer mentions %s, which is one of the limits that makes it narrow", part)
		}
	}

	// A rule nobody can read is not recorded, so the prose has to still be
	// prose. This is a floor, not a style: #4's constant carries roughly this
	// much reasoning and this rule is a larger decision than that one.
	if len(doc) < 1500 {
		t.Errorf("the rule beside fetchSiteVouches is %d characters, which is too short to be the reasoning it is supposed to record", len(doc))
	}
}

// How the guard reads the origin rule: through the one function that decides
// it, with the request itself, and without short-circuiting the token check.
//
// Both halves matter and neither is visible in a response. Passing something
// pre-massaged to the policy, or computing the origin check only when the
// token check already failed, would leave every behavioural test in this
// package green: the first because the policy would still say no to a hostile
// origin, the second because the answer would still be the uniform rejection
// and only its timing would differ.
func TestTheGuardAsksTheOriginPolicyAboutTheWholeRequest(t *testing.T) {
	t.Parallel()
	fileSet, files := parsePackage(t)

	const (
		tokenCheck  = "g.token.matches(presentedToken(r))"
		originCheck = "g.origins.allowsRequest(r)"
	)

	var wrap *ast.FuncDecl
	for _, file := range files {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if ok && function.Name.Name == "wrap" && function.Recv != nil {
				wrap = function
			}
		}
	}
	if wrap == nil {
		t.Fatal("guard.wrap no longer exists under that name, so this test is not checking the checks")
	}

	// What each check is assigned from, in the order the assignments appear.
	assigned := map[string]string{}
	var order []string
	ast.Inspect(wrap.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		name := source(t, fileSet, assignment.Lhs[0])
		if name != "tokenOK" && name != "originOK" {
			return true
		}
		assigned[name] = source(t, fileSet, assignment.Rhs[0])
		order = append(order, name)
		return true
	})

	if got := assigned["tokenOK"]; got != tokenCheck {
		t.Errorf("the token check is %q, want exactly %q", got, tokenCheck)
	}
	if got := assigned["originOK"]; got != originCheck {
		t.Errorf("the origin check is %q, want exactly %q; the policy decides about the request, so a caller cannot hand it a header list that has already been interpreted", got, originCheck)
	}
	if len(order) != 2 {
		t.Fatalf("guard.wrap assigns %v, want both checks computed once each", order)
	}

	// Neither check may be guarded by the other: both are computed as plain
	// statements, so the only combination left is the one that runs both.
	ast.Inspect(wrap.Body, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		written := source(t, fileSet, binary)
		if strings.Contains(written, tokenCheck) || strings.Contains(written, originCheck) {
			t.Errorf("guard.wrap combines a check into %q, which short-circuits it; the results are computed first and only then combined", written)
		}
		return true
	})
}
