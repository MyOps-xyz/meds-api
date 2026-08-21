package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sync"

	"gopkg.in/yaml.v3"

	openapi "github.com/MyOps-xyz/meds-api/api"
)

// specYAML est le contrat OpenAPI, embarqué dans le binaire par le paquet
// api/ — en un seul exemplaire, à son emplacement conventionnel.
var specYAML = openapi.Spec

var (
	specOnce sync.Once
	specJSON []byte
	specETag string
	specErr  error
)

// specAsJSON convertit le contrat YAML en JSON, une seule fois.
//
// La conversion est faite au premier appel plutôt qu'à la compilation :
// elle coûte quelques millisecondes, sur un endpoint appelé rarement, et
// éviter une étape de génération supprime toute possibilité que le JSON
// servi diverge du YAML versionné.
func specAsJSON() ([]byte, error) {
	specOnce.Do(func() {
		var doc any
		if err := yaml.Unmarshal(specYAML, &doc); err != nil {
			specErr = fmt.Errorf("contrat OpenAPI illisible : %w", err)
			return
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			specErr = fmt.Errorf("contrat OpenAPI non sérialisable : %w", err)
			return
		}
		specJSON = buf.Bytes()
		// Le contrat est immuable pour la durée du processus : son ETag est
		// calculé ici, et non par requête — hacher 40 Ko à chaque appel est
		// précisément le travail qu'un 304 est censé éviter.
		specETag = `"` + etagOf(string(specJSON)) + `"`
	})
	return specJSON, specErr
}

// OpenAPI sert le contrat au format JSON.
func (h *Handlers) OpenAPI(w http.ResponseWriter, r *http.Request) {
	doc, err := specAsJSON()
	if err != nil {
		WriteProblem(w, r, ErrInternal, "Le contrat OpenAPI n'a pas pu être servi.")
		return
	}
	head := w.Header()
	head.Set("Content-Type", "application/json; charset=utf-8")
	head.Set("Cache-Control", "public, max-age=3600")
	head.Set("ETag", specETag)
	if matchesETag(r.Header.Get("If-None-Match"), specETag) {
		head.Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(doc)
}

// docsPage est la documentation interactive.
//
// Elle fonctionne **hors ligne** : ni CDN, ni police distante, ni script
// tiers. C'est une exigence explicite (T-33) et non un raffinement — une
// page de documentation qui ne s'affiche pas sur un réseau cloisonné est
// une page de documentation inutile, et charger un script tiers dans la page
// qui manipule des clés API serait par ailleurs discutable.
//
// La contrepartie est assumée : le rendu est plus modeste qu'avec Scalar ou
// Redoc, mais il est complet et lisible.
const docsPageHead = `<!doctype html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>meds-api — documentation</title>
<style>
:root {
  --fg: #1a1a1a; --bg: #ffffff; --muted: #666; --line: #e3e3e3;
  --accent: #0b6bcb; --code-bg: #f6f7f9; --warn-bg: #fff8e6; --warn-line: #e0b000;
  --get: #0b6bcb; --post: #b06000;
}
@media (prefers-color-scheme: dark) {
  :root {
    --fg: #e6e6e6; --bg: #16181c; --muted: #9aa0a6; --line: #2c2f36;
    --accent: #64a8ee; --code-bg: #1e2127; --warn-bg: #2a2410; --warn-line: #8a6d00;
    --get: #64a8ee; --post: #e0973a;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; padding: 0; color: var(--fg); background: var(--bg);
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}
.wrap { max-width: 62rem; margin: 0 auto; padding: 2rem 1.25rem 6rem; }
h1 { font-size: 1.9rem; margin: 0 0 .25rem; letter-spacing: -0.02em; }
h2 { font-size: 1.25rem; margin: 2.5rem 0 .75rem; padding-bottom: .35rem; border-bottom: 1px solid var(--line); }
h3 { font-size: 1rem; margin: 1.75rem 0 .35rem; }
p { margin: .5rem 0; }
.sub { color: var(--muted); margin-bottom: 1.5rem; }
code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .875em; }
code { background: var(--code-bg); padding: .1em .35em; border-radius: 3px; }
pre { background: var(--code-bg); padding: .85rem 1rem; border-radius: 6px; overflow-x: auto; }
pre code { background: none; padding: 0; }
.warn {
  background: var(--warn-bg); border-left: 3px solid var(--warn-line);
  padding: .85rem 1rem; margin: 1.25rem 0; border-radius: 0 4px 4px 0;
}
.op { border: 1px solid var(--line); border-radius: 6px; margin: .6rem 0; overflow: hidden; }
.op > summary {
  cursor: pointer; padding: .65rem .9rem; display: flex; gap: .7rem; align-items: baseline;
  list-style: none;
}
.op > summary::-webkit-details-marker { display: none; }
.op > summary:hover { background: var(--code-bg); }
.verb { font-weight: 700; font-size: .75rem; letter-spacing: .06em; min-width: 3.2rem; }
.verb.get { color: var(--get); }
.verb.post { color: var(--post); }
.path { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9rem; }
.summary { color: var(--muted); font-size: .875rem; margin-left: auto; text-align: right; }
.body { padding: 0 .9rem 1rem; border-top: 1px solid var(--line); }
table { border-collapse: collapse; width: 100%; margin: .6rem 0; font-size: .875rem; }
th, td { text-align: left; padding: .4rem .6rem; border-bottom: 1px solid var(--line); vertical-align: top; }
th { color: var(--muted); font-weight: 600; }
.req { color: #c0392b; font-size: .75rem; }
.tag { color: var(--muted); font-size: .8rem; text-transform: uppercase; letter-spacing: .06em; }
a { color: var(--accent); }
footer { margin-top: 3rem; padding-top: 1rem; border-top: 1px solid var(--line); color: var(--muted); font-size: .875rem; }
</style>
</head>
<body>
<div class="wrap">
`

// docsPageFoot ferme la page. La séparation en deux constantes, plutôt qu'un
// gabarit à substitution, évite que les « % » du CSS (width: 100%) ne soient
// pris pour des verbes de format.
const docsPageFoot = `<footer>
Contrat brut : <a href="/openapi.json">/openapi.json</a> —
état du jeu de données : <code>GET /v1/dataset</code>.
Cette page est entièrement autonome : elle ne charge aucune ressource externe.
</footer>
</div>
</body>
</html>
`

var (
	docsOnce sync.Once
	docsHTML []byte
	docsErr  error
)

// Docs sert la documentation interactive.
func (h *Handlers) Docs(w http.ResponseWriter, r *http.Request) {
	docsOnce.Do(func() { docsHTML, docsErr = renderDocs() })
	if docsErr != nil {
		WriteProblem(w, r, ErrInternal, "La documentation n'a pas pu être générée.")
		return
	}
	head := w.Header()
	head.Set("Content-Type", "text/html; charset=utf-8")
	head.Set("Cache-Control", "public, max-age=3600")
	// La page est statique et n'exécute aucun script : la politique la plus
	// stricte possible s'applique, styles en ligne mis à part.
	head.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	_, _ = w.Write(docsHTML)
}

// spec est la vue minimale du contrat nécessaire au rendu.
type spec struct {
	Info struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Summary     string `yaml:"summary"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	Paths map[string]map[string]specOp `yaml:"paths"`
}

type specOp struct {
	Tags        []string `yaml:"tags"`
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description"`
	Parameters  []struct {
		Name        string `yaml:"name"`
		In          string `yaml:"in"`
		Required    bool   `yaml:"required"`
		Description string `yaml:"description"`
		Ref         string `yaml:"$ref"`
	} `yaml:"parameters"`
	Responses map[string]struct {
		Description string `yaml:"description"`
	} `yaml:"responses"`
}

func renderDocs() ([]byte, error) {
	var s spec
	if err := yaml.Unmarshal(specYAML, &s); err != nil {
		return nil, err
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "<h1>%s <span class=\"sub\">v%s</span></h1>\n",
		html.EscapeString(s.Info.Title), html.EscapeString(s.Info.Version))
	fmt.Fprintf(&b, "<p class=\"sub\">%s</p>\n", html.EscapeString(s.Info.Summary))

	// L'avertissement obligatoire est rendu en premier et mis en évidence :
	// c'est la seule information de cette page qui engage la responsabilité
	// de l'intégrateur.
	fmt.Fprintf(&b, "<div class=\"warn\"><strong>Avertissement.</strong> %s</div>\n",
		html.EscapeString(Disclaimer))

	fmt.Fprint(&b, "<h2>Authentification</h2>\n")
	fmt.Fprint(&b, "<p>Toutes les routes <code>/v1/*</code> exigent une clé API :</p>\n")
	fmt.Fprint(&b, "<pre><code>curl -H \"Authorization: Bearer &lt;votre-clé&gt;\" \\\n"+
		"  https://exemple.org/v1/medicaments?q=doliprane</code></pre>\n")
	fmt.Fprint(&b, "<p>Exemptés : <code>/healthz</code>, <code>/readyz</code>, "+
		"<code>/metrics</code>, <code>/openapi.json</code>, <code>/docs</code>. "+
		"<code>POST /admin/sync</code> exige une clé d'administration distincte.</p>\n")

	fmt.Fprint(&b, "<h2>Conventions</h2>\n<ul>\n")
	for _, line := range []string{
		"Les montants sont des <strong>entiers en centimes</strong>, suffixés <code>_cents</code>.",
		"L'absence de valeur est toujours <code>null</code>, jamais <code>\"\"</code> ni <code>0</code>. " +
			"7 276 des 20 903 présentations n'ont aucun prix : les confondre avec un prix nul fausserait toute statistique.",
		"Les valeurs métier sont conservées telles quelles (<code>Autorisation active</code>).",
		"Les erreurs suivent la <strong>RFC 9457</strong> (<code>application/problem+json</code>) et portent toujours un <code>request_id</code>.",
		"La pagination se fait par curseur opaque, invalidé si le jeu de données est rechargé en cours de parcours.",
	} {
		fmt.Fprintf(&b, "<li>%s</li>\n", line)
	}
	fmt.Fprint(&b, "</ul>\n")

	// Les endpoints sont regroupés par étiquette, dans l'ordre de leur
	// première apparition : celui du contrat, qui va du plus courant au plus
	// spécialisé.
	order := []string{}
	byTag := map[string][]string{}
	paths := sortedPathKeys(s.Paths)
	for _, path := range paths {
		for _, method := range []string{"get", "post"} {
			op, ok := s.Paths[path][method]
			if !ok {
				continue
			}
			tag := "Autres"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			if _, seen := byTag[tag]; !seen {
				order = append(order, tag)
			}
			byTag[tag] = append(byTag[tag], method+" "+path)
		}
	}

	for _, tag := range order {
		fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(tag))
		for _, key := range byTag[tag] {
			method, path := splitMethodPath(key)
			op := s.Paths[path][method]
			renderOp(&b, method, path, op)
		}
	}
	var page bytes.Buffer
	page.Grow(len(docsPageHead) + b.Len() + len(docsPageFoot))
	page.WriteString(docsPageHead)
	page.Write(b.Bytes())
	page.WriteString(docsPageFoot)
	return page.Bytes(), nil
}

func renderOp(b *bytes.Buffer, method, path string, op specOp) {
	fmt.Fprintf(b, "<details class=\"op\"><summary>"+
		"<span class=\"verb %s\">%s</span>"+
		"<span class=\"path\">%s</span>"+
		"<span class=\"summary\">%s</span></summary>\n<div class=\"body\">\n",
		method, upper(method), html.EscapeString(path), html.EscapeString(op.Summary))

	if op.Description != "" {
		fmt.Fprintf(b, "<p>%s</p>\n", paragraphs(op.Description))
	}

	if len(op.Parameters) > 0 {
		fmt.Fprint(b, "<h3>Paramètres</h3>\n<table><tr><th>Nom</th><th>Où</th><th>Description</th></tr>\n")
		for _, p := range op.Parameters {
			name, in, desc := p.Name, p.In, p.Description
			if name == "" && p.Ref != "" {
				// Paramètre partagé : seul son nom est utile ici, le détail
				// figure dans le contrat brut.
				name = refName(p.Ref)
				in = "query"
			}
			req := ""
			if p.Required {
				req = " <span class=\"req\">requis</span>"
			}
			fmt.Fprintf(b, "<tr><td><code>%s</code>%s</td><td>%s</td><td>%s</td></tr>\n",
				html.EscapeString(name), req, html.EscapeString(in), html.EscapeString(desc))
		}
		fmt.Fprint(b, "</table>\n")
	}

	if len(op.Responses) > 0 {
		fmt.Fprint(b, "<h3>Réponses</h3>\n<table><tr><th>Code</th><th>Description</th></tr>\n")
		for _, code := range sortedResponseKeys(op.Responses) {
			fmt.Fprintf(b, "<tr><td><code>%s</code></td><td>%s</td></tr>\n",
				html.EscapeString(code), html.EscapeString(op.Responses[code].Description))
		}
		fmt.Fprint(b, "</table>\n")
	}
	fmt.Fprint(b, "</div></details>\n")
}

// paragraphs convertit un texte Markdown minimal en HTML : seuls les
// paragraphes, le gras et le code sont reconnus. Tout le reste est échappé —
// le contrat est une source de confiance, mais rien ne justifie d'y injecter
// du HTML arbitraire.
func paragraphs(s string) string {
	escaped := html.EscapeString(s)
	out := bytes.Buffer{}
	inCode := false
	inBold := false
	for i := 0; i < len(escaped); i++ {
		switch {
		case escaped[i] == '`':
			if inCode {
				out.WriteString("</code>")
			} else {
				out.WriteString("<code>")
			}
			inCode = !inCode
		case escaped[i] == '*' && i+1 < len(escaped) && escaped[i+1] == '*':
			if inBold {
				out.WriteString("</strong>")
			} else {
				out.WriteString("<strong>")
			}
			inBold = !inBold
			i++
		case escaped[i] == '\n' && i+1 < len(escaped) && escaped[i+1] == '\n':
			out.WriteString("</p><p>")
			i++
		default:
			out.WriteByte(escaped[i])
		}
	}
	if inCode {
		out.WriteString("</code>")
	}
	if inBold {
		out.WriteString("</strong>")
	}
	return out.String()
}

func sortedPathKeys(m map[string]map[string]specOp) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortedResponseKeys(m map[string]struct {
	Description string `yaml:"description"`
}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func splitMethodPath(key string) (method, path string) {
	for i := 0; i < len(key); i++ {
		if key[i] == ' ' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

func refName(ref string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return ref[i+1:]
		}
	}
	return ref
}

func upper(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'a' && out[i] <= 'z' {
			out[i] -= 'a' - 'A'
		}
	}
	return string(out)
}
