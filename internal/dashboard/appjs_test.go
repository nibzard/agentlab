package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// appJSSource returns the shipped dashboard script. The tests below treat it
// as the final code path for findings F3 (T07, T08): if they fail, the UI has
// regressed to string-built HTML around untrusted values.
func appJSSource(t *testing.T) string {
	t.Helper()
	data, err := staticFiles.ReadFile("static/assets/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	return string(data)
}

// extractJSFunction returns the source of the named function, from its
// declaration through its balanced closing brace.
func extractJSFunction(t *testing.T, src, name string) string {
	t.Helper()
	decl := "function " + name + "("
	idx := strings.Index(src, decl)
	if idx < 0 {
		t.Fatalf("function %s not found in app.js", name)
	}
	open := strings.Index(src[idx:], "{")
	if open < 0 {
		t.Fatalf("function %s has no body", name)
	}
	depth := 0
	for i := idx + open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[idx : i+1]
			}
		}
	}
	t.Fatalf("function %s has unbalanced braces", name)
	return ""
}

// TestExposuresTableBuiltWithDOMAPIs covers T07: the Exposures table must be
// built with createElement/textContent, and the Remove handler must be attached
// with addEventListener. A name containing quotes then renders as text and
// cannot break out into an attribute or handler string. The browser-side half
// of the criterion (a quote-laden name rendering and Remove working) follows
// from these properties and is asserted structurally here; the package has no
// JavaScript runtime to execute the page.
func TestExposuresTableBuiltWithDOMAPIs(t *testing.T) {
	body := extractJSFunction(t, appJSSource(t), "loadExposures")

	if !strings.Contains(body, "document.createElement") {
		t.Error("loadExposures does not build rows with document.createElement")
	}
	if !strings.Contains(body, "textContent") {
		t.Error("loadExposures does not fill cells with textContent")
	}
	// The Remove button is attached through addActionButton, which must use
	// addEventListener (no inline handler attribute).
	if !strings.Contains(body, `addActionButton(actions, "Remove"`) {
		t.Error("loadExposures does not attach its Remove button through addActionButton")
	}
	if btn := extractJSFunction(t, appJSSource(t), "addActionButton"); !strings.Contains(btn, "addEventListener") {
		t.Error("addActionButton does not use addEventListener")
	}
	// The name must reach the server through the closure, not a string.
	if !strings.Contains(body, "removeExposure(name)") {
		t.Error("loadExposures does not pass the exposure name to removeExposure as a value")
	}
	if strings.Contains(body, "esc(") {
		t.Error("loadExposures still interpolates values through esc(); untrusted values must go through textContent")
	}
	if strings.Contains(strings.ToLower(body), "onclick") {
		t.Error("loadExposures still references onclick")
	}
	// Any innerHTML use inside the builder may only clear the tbody.
	clearRe := regexp.MustCompile(`\.innerHTML\s*=\s*("[^"]*"|'[^']*')`)
	for _, m := range clearRe.FindAllStringSubmatch(body, -1) {
		if m[1] != `""` && m[1] != `''` {
			t.Errorf("loadExposures assigns non-empty innerHTML %s; rows must be appended as DOM nodes", m[1])
		}
	}
}

// TestAppJSNoEscInAttributeOrHandlerContexts covers T08: no esc() result may
// be interpolated into an HTML attribute value or an event-handler string.
// esc() encodes quotes, but the only contexts proven safe for it are HTML text
// nodes; attribute and handler contexts are banned outright.
func TestAppJSNoEscInAttributeOrHandlerContexts(t *testing.T) {
	src := appJSSource(t)

	// No event-handler attribute may be constructed anywhere in the script.
	if regexp.MustCompile(`\son[a-z]+\s*=`).MatchString(src) {
		t.Error("app.js constructs an inline event-handler attribute (on...=); use addEventListener")
	}
	// No attribute value may open immediately before an esc() concatenation,
	// in double-quoted form (=" + esc() or =\" + esc()) or single-quoted form
	// (=' + esc()).
	attrOpenRe := regexp.MustCompile(`=\\?["']["']?\s*\+\s*esc\(`)
	if loc := attrOpenRe.FindString(src); loc != "" {
		t.Errorf("app.js interpolates esc() into an attribute value near %q", loc)
	}
	// Handlers must not be re-exposed as window globals, which inline
	// onclick attributes would need.
	if regexp.MustCompile(`window\.[A-Za-z_$][A-Za-z0-9_$]*\s*=`).MatchString(src) {
		t.Error("app.js assigns a function to a window global; handlers must stay closure-local")
	}
}

// TestEscInterpolationDetectorCatchesKnownBadPatterns proves the detectors
// above fail on the original F3 sink shapes, so they cannot be weakened
// silently.
func TestEscInterpolationDetectorCatchesKnownBadPatterns(t *testing.T) {
	handlerRe := regexp.MustCompile(`\son[a-z]+\s*=`)
	attrOpenRe := regexp.MustCompile(`=\\?["']["']?\s*\+\s*esc\(`)

	cases := []struct {
		name     string
		code     string
		detector func(string) bool
	}{
		{
			// The original exposures Remove button (F3, app.js:511). The esc()
			// result lands inside an onclick handler string.
			name:     "onclick handler string",
			code:     `"<td>" + '<button class="btn" onclick="removeExposure(\'' + esc(ex.name) + "')\">Remove</button>" + "</td>"`,
			detector: handlerRe.MatchString,
		},
		{
			// The original exposures URL cell.
			name:     "double-quoted attribute",
			code:     `'<a class="exposure-url" href="' + esc(ex.url) + '" target="_blank">' + esc(ex.url) + "</a>"`,
			detector: attrOpenRe.MatchString,
		},
		{
			// Escaped double quotes inside a double-quoted literal.
			name:     "escaped-quote attribute",
			code:     `"<a href=\"" + esc(u) + "\">link</a>"`,
			detector: attrOpenRe.MatchString,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.detector(tc.code) {
				t.Errorf("detector missed the known-bad pattern: %s", tc.code)
			}
		})
	}
}
