package bouncer

import (
	"encoding/base64"
	"math/rand"
	"strings"
)

// Fake secrets for tests.
//
// GitHub push protection rejects commits that contain realistic-looking
// credentials, so no test or corpus file holds one literally: every value is
// assembled at run time from split prefixes and deterministic random bodies.
// None of these values is a real credential.

const (
	alphaNum   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upperNum   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	urlSafe    = alphaNum + "_-"
	base64Std  = alphaNum + "+/"
	lowerAlpha = "abcdefghijklmnopqrstuvwxyz"
)

type fakeGen struct{ rng *rand.Rand }

func newFakeGen(seed int64) *fakeGen { return &fakeGen{rng: rand.New(rand.NewSource(seed))} }

func (g *fakeGen) chars(alphabet string, n int) string {
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[g.rng.Intn(len(alphabet))])
	}
	return b.String()
}

// join concatenates parts; used so that no prefix appears in source as a
// single literal next to a body.
func join(parts ...string) string { return strings.Join(parts, "") }

func (g *fakeGen) awsKeyID() string     { return join("AK", "IA", g.chars(upperNum, 16)) }
func (g *fakeGen) awsTempKeyID() string { return join("AS", "IA", g.chars(upperNum, 16)) }
func (g *fakeGen) awsSecret() string    { return g.chars(base64Std, 40) }
func (g *fakeGen) githubPAT() string    { return join("gh", "p_", g.chars(alphaNum, 36)) }
func (g *fakeGen) githubOAuth() string  { return join("gh", "o_", g.chars(alphaNum, 36)) }
func (g *fakeGen) githubFine() string {
	return join("github", "_pat_", g.chars(alphaNum, 22), "_", g.chars(alphaNum, 59))
}
func (g *fakeGen) gitlabPAT() string { return join("gl", "pat-", g.chars(urlSafe, 20)) }
func (g *fakeGen) stripeLive() string {
	return join("sk", "_live_", g.chars(alphaNum, 99))
}
func (g *fakeGen) stripeTest() string { return join("sk", "_test_", g.chars(alphaNum, 40)) }
func (g *fakeGen) stripeRestricted() string {
	return join("rk", "_live_", g.chars(alphaNum, 40))
}
func (g *fakeGen) openAILegacy() string { return join("sk", "-", g.chars(alphaNum, 48)) }
func (g *fakeGen) openAIProject() string {
	return join("sk", "-proj-", g.chars(urlSafe, 120))
}
func (g *fakeGen) anthropic() string {
	return join("sk", "-ant-", "api03-", g.chars(urlSafe, 93))
}
func (g *fakeGen) googleAPIKey() string { return join("AI", "za", g.chars(urlSafe, 35)) }
func (g *fakeGen) gcpOAuth() string     { return join("ya", "29.", g.chars(urlSafe, 60)) }
func (g *fakeGen) slackBot() string {
	return join("xo", "xb-", g.chars("0123456789", 12), "-", g.chars("0123456789", 13), "-", g.chars(alphaNum, 24))
}
func (g *fakeGen) slackWebhook() string {
	return join("https://hooks.", "slack.com/services/", "T", g.chars(upperNum, 10), "/B", g.chars(upperNum, 10), "/", g.chars(alphaNum, 24))
}
func (g *fakeGen) npmToken() string { return join("np", "m_", g.chars(alphaNum, 36)) }
func (g *fakeGen) jwt() string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pl := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"` + g.chars(alphaNum, 12) + `","iat":1700000000}`))
	return hdr + "." + pl + "." + g.chars(urlSafe, 43)
}
func (g *fakeGen) basicCreds() string {
	return base64.StdEncoding.EncodeToString([]byte("svc-" + g.chars(lowerAlpha, 6) + ":" + g.chars(alphaNum, 16)))
}
func (g *fakeGen) password() string {
	return g.chars(lowerAlpha, 6) + "-" + g.chars(alphaNum, 10) + "!"
}
func (g *fakeGen) dsnPassword() string { return g.chars(alphaNum, 18) }

// pemBlock builds a PEM-armored private key with the given label ("RSA
// PRIVATE KEY", "PGP PRIVATE KEY BLOCK", ...) and a random body; body is
// returned separately so tests can check it no longer appears.
func (g *fakeGen) pemBlock(label string) (block, body string) {
	lines := make([]string, 0, 6)
	for i := 0; i < 5; i++ {
		lines = append(lines, g.chars(base64Std, 64))
	}
	body = strings.Join(lines, "\n")
	dashes := "-----"
	block = dashes + "BEGIN " + label + dashes + "\n" + body + "\n" + dashes + "END " + label + dashes
	return block, body
}
