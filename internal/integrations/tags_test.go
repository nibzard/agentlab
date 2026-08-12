package integrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTags_LowercasesAndTrims(t *testing.T) {
	got, err := NormalizeTags([]string{"  Prod ", "WEB", "\tApi"})
	require.NoError(t, err)
	assert.Equal(t, []string{"prod", "web", "api"}, got)
}

func TestNormalizeTags_DedupesStableOrder(t *testing.T) {
	got, err := NormalizeTags([]string{"web", "prod", "WEB", "Prod", "web"})
	require.NoError(t, err)
	assert.Equal(t, []string{"web", "prod"}, got)
}

func TestNormalizeTags_DropsEmpty(t *testing.T) {
	got, err := NormalizeTags([]string{"", "  ", "web", ""})
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, got)
}

func TestNormalizeTags_RejectsInvalid(t *testing.T) {
	for _, bad := range []string{
		"has,comma", "has space", "tab\there", "new\nline", "ctrl\x01",
		strings.Repeat("a", maxTagLength+1),
	} {
		t.Run(bad, func(t *testing.T) {
			_, err := NormalizeTags([]string{bad})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid tag")
		})
	}
}

func TestNormalizeTags_AtMaxLength(t *testing.T) {
	tag := strings.Repeat("a", maxTagLength)
	got, err := NormalizeTags([]string{tag})
	require.NoError(t, err)
	assert.Equal(t, []string{tag}, got)
}

func TestJoinTagsRoundTrip(t *testing.T) {
	normalized, err := NormalizeTags([]string{"Prod", "WEB", "prod"})
	require.NoError(t, err)
	joined := JoinTags(normalized)
	assert.Equal(t, "prod,web", joined)
	// Splitting on the delimiter inverts JoinTags for normalized values.
	assert.Equal(t, normalized, strings.Split(joined, ","))
}

func TestNormalizeTagSelector(t *testing.T) {
	got, err := NormalizeTagSelector("  Prod ")
	require.NoError(t, err)
	assert.Equal(t, "prod", got)

	_, err = NormalizeTagSelector("")
	require.Error(t, err)
}

// TestMatchesSandbox_CaseInsensitive proves that after normalization a tag-mode
// integration attaches regardless of the original letter case supplied on
// either side (review M6).
func TestMatchesSandbox_CaseInsensitive(t *testing.T) {
	selector, err := NormalizeTagSelector("Prod")
	require.NoError(t, err)
	integ := &Integration{AttachMode: AttachTag, AttachSelector: selector}

	// Sandbox tags stored lowercased (the persisted form).
	assert.True(t, integ.MatchesSandbox("any-name", []string{"prod", "web"}))
	// Non-matching tag does not attach.
	assert.False(t, integ.MatchesSandbox("any-name", []string{"web", "staging"}))
}

// TestMatchesSandbox_TagSelectorsEndToEnd covers the create-time normalization
// a daemon would apply: a raw selector and raw sandbox tags both normalize to
// the same lowercased value and therefore match.
func TestMatchesSandbox_TagSelectorsEndToEnd(t *testing.T) {
	rawSelector := "  TEAM-Backend "
	selector, err := NormalizeTagSelector(rawSelector)
	require.NoError(t, err)
	integ := &Integration{AttachMode: AttachTag, AttachSelector: selector}

	sandboxTags, err := NormalizeTags([]string{"TEAM-Backend", "region-us"})
	require.NoError(t, err)

	assert.True(t, integ.MatchesSandbox("sb-1", sandboxTags))
}
