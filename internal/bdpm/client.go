// Package bdpm télécharge, décode et lit les fichiers plats de la Base de
// Données Publique des Médicaments (ANSM). Ce paquet ne connaît rien du
// serveur HTTP applicatif ni du Store en mémoire : le sens des dépendances
// du projet est strictement api → store → bdpm (docs/02-architecture.md).
package bdpm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// FileDescriptor identifie un fichier téléchargeable de la BDPM par son nom
// canonique et son chemin propre sous /download/. Modéliser le chemin comme
// un champ à part, plutôt que de le reconstruire par concaténation à partir
// du seul nom, est ce qui permet de représenter l'exception documentée
// (docs/01-analyse-source-bdpm.md §1) : les informations importantes sont
// servies sur /download/CIS_InfoImportantes.txt, hors du répertoire
// /download/file/ où vivent les dix autres fichiers.
type FileDescriptor struct {
	// Name est le nom canonique du fichier (ex. "CIS_bdpm.txt"), utilisé
	// comme identifiant dans les résultats, le manifest et la quarantaine.
	Name string
	// Path est le chemin relatif à la racine /download/ de l'ANSM.
	Path string
}

// baseDownloadURL est la racine de téléchargement de l'ANSM
// (docs/01-analyse-source-bdpm.md §1). Le Client accepte de la surcharger
// (WithBaseURL) pour les tests, contre un httptest.Server local.
const baseDownloadURL = "https://base-donnees-publique.medicaments.gouv.fr/download"

// DefaultFiles retourne les descripteurs des dix fichiers à ingérer
// quotidiennement, dans l'ordre documenté par
// docs/01-analyse-source-bdpm.md §2. Le fichier des informations
// importantes (CIS_InfoImportantes.txt) n'y figure délibérément pas : il
// est hors périmètre d'ingestion, mais reste représentable via
// InfoImportantesFile pour un usage futur.
func DefaultFiles() []FileDescriptor {
	names := []string{
		"CIS_bdpm.txt",
		"CIS_CIP_bdpm.txt",
		"CIS_COMPO_bdpm.txt",
		"CIS_HAS_SMR_bdpm.txt",
		"CIS_HAS_ASMR_bdpm.txt",
		"HAS_LiensPageCT_bdpm.txt",
		"CIS_GENER_bdpm.txt",
		"CIS_CPD_bdpm.txt",
		"CIS_CIP_Dispo_Spec.txt",
		"CIS_MITM.txt",
	}
	files := make([]FileDescriptor, len(names))
	for i, name := range names {
		files[i] = FileDescriptor{Name: name, Path: "file/" + name}
	}
	return files
}

// InfoImportantesFile décrit le onzième fichier de la BDPM, servi à un
// chemin distinct des dix autres (voir FileDescriptor). Il n'est pas inclus
// dans DefaultFiles : ce n'est pas un fichier de médicaments à proprement
// parler, et son nom réel comporte un horodatage variable
// (CIS_InfoImportantes_AAAAMMJJhhmiss_bdpm.txt) que l'ANSM masque derrière
// ce chemin stable.
var InfoImportantesFile = FileDescriptor{
	Name: "CIS_InfoImportantes.txt",
	Path: "CIS_InfoImportantes.txt",
}

// FileResult est le résultat du téléchargement d'un fichier unique.
type FileResult struct {
	// Name reprend FileDescriptor.Name.
	Name string
	// Bytes est le contenu brut du fichier, non décodé (voir DecodeFile).
	Bytes []byte
	// SHA256 est l'empreinte hexadécimale du contenu, calculée au fil du
	// téléchargement (io.TeeReader) : c'est la seule détection de
	// changement possible, l'ANSM ne servant ni ETag ni Last-Modified
	// (docs/01-analyse-source-bdpm.md, piège P2).
	SHA256 string
	// Duration est le temps total passé sur ce fichier, tentatives ratées
	// comprises.
	Duration time.Duration
	// Attempts est le nombre de tentatives HTTP effectuées (>= 1).
	Attempts int
}

// DatasetResult est le résultat agrégé du téléchargement de l'ensemble des
// fichiers demandés.
type DatasetResult struct {
	// Files contient un résultat par fichier, dans l'ordre où les
	// descripteurs ont été fournis à FetchAll (et non un ordre de
	// complétion, pour rester déterministe indépendamment du
	// parallélisme).
	Files []FileResult
	// Hash est le hash global du jeu de données : SHA-256 de la
	// concaténation des empreintes SHA-256 de chaque fichier, elles-mêmes
	// triées par nom de fichier pour garantir le déterminisme quel que
	// soit l'ordre d'entrée ou de complétion.
	Hash string
	// Duration est la durée totale de FetchAll.
	Duration time.Duration
}

// Client télécharge les fichiers de la BDPM avec les garanties de
// docs/09-pipeline-mise-a-jour.md §3.1 : parallélisme borné, tentatives
// avec repli exponentiel aléatoire, délais stricts, TLS non dégradable,
// lecture bornée en mémoire.
type Client struct {
	httpClient *http.Client
	baseURL    string
	userAgent  string

	maxFileBytes int64

	maxAttempts   int
	backoffBase   time.Duration
	backoffFactor int
	jitter        func(base time.Duration) time.Duration

	concurrency int

	perFileTimeout time.Duration
	globalTimeout  time.Duration
}

// Valeurs par défaut du Client, toutes issues de
// docs/09-pipeline-mise-a-jour.md §3.1.
const (
	defaultMaxFileBytes   = 100 << 20 // 100 Mio par fichier
	defaultMaxAttempts    = 3
	defaultBackoffBase    = 2 * time.Second
	defaultBackoffFactor  = 4 // 2s, 8s, 32s : 2 * 4^(n-1)
	defaultConcurrency    = 4
	defaultPerFileTimeout = 2 * time.Minute
	defaultGlobalTimeout  = 5 * time.Minute

	// maxReasonableRetryAfter borne la confiance accordée à l'en-tête
	// Retry-After : un serveur (ou un attaquant sur un miroir compromis)
	// qui demanderait une attente extravagante ne doit pas geler toute la
	// synchronisation. Au-delà de ce plafond, le repli exponentiel normal
	// s'applique à la place.
	maxReasonableRetryAfter = 2 * time.Minute
)

// Option configure un Client construit par NewClient.
type Option func(*Client)

// WithHTTPClient remplace le client HTTP interne. Réservé aux tests : en
// production, NewClient construit toujours un client doté d'un Transport
// explicite (voir newTransport), jamais http.DefaultClient.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithBaseURL remplace la racine de téléchargement, pour pointer vers un
// httptest.Server dans les tests.
func WithBaseURL(base string) Option {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(base, "/") }
}

// WithUserAgent remplace l'en-tête User-Agent envoyé à l'ANSM.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// WithMaxFileBytes borne la taille lue par fichier (io.LimitReader).
func WithMaxFileBytes(n int64) Option {
	return func(c *Client) { c.maxFileBytes = n }
}

// WithMaxAttempts fixe le nombre de tentatives par fichier (au moins 1).
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.maxAttempts = n
	}
}

// WithConcurrency fixe le nombre de téléchargements simultanés
// (errgroup.SetLimit).
func WithConcurrency(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.concurrency = n
	}
}

// WithBackoff fixe la base et le facteur multiplicatif du repli
// exponentiel entre tentatives (hors aléa).
func WithBackoff(base time.Duration, factor int) Option {
	return func(c *Client) {
		c.backoffBase = base
		if factor < 1 {
			factor = 1
		}
		c.backoffFactor = factor
	}
}

// WithJitterFunc remplace la fonction d'aléa appliquée au repli entre
// tentatives. C'est ce qui rend l'aléa du jitter injectable, condition
// nécessaire pour des tests déterministes (docs/09-pipeline-mise-a-jour.md
// §3.1) : un test fournit typiquement `func(time.Duration) time.Duration {
// return 0 }` pour supprimer toute variabilité de durée.
func WithJitterFunc(f func(base time.Duration) time.Duration) Option {
	return func(c *Client) { c.jitter = f }
}

// WithPerFileTimeout fixe le délai maximal accordé à un seul fichier,
// tentatives comprises.
func WithPerFileTimeout(d time.Duration) Option {
	return func(c *Client) { c.perFileTimeout = d }
}

// WithGlobalTimeout fixe le délai maximal accordé à l'ensemble de la
// synchronisation.
func WithGlobalTimeout(d time.Duration) Option {
	return func(c *Client) { c.globalTimeout = d }
}

// NewClient construit un Client prêt à l'emploi. version identifie le
// binaire dans l'en-tête User-Agent envoyé à l'ANSM (identification loyale,
// docs/09-pipeline-mise-a-jour.md §3.1, exigence E4) ; il peut être
// remplacé explicitement via WithUserAgent.
func NewClient(version string, opts ...Option) *Client {
	c := &Client{
		httpClient:     &http.Client{Transport: newTransport()},
		baseURL:        baseDownloadURL,
		userAgent:      fmt.Sprintf("meds-api/%s (+https://github.com/MyOps-xyz/meds-api)", version),
		maxFileBytes:   defaultMaxFileBytes,
		maxAttempts:    defaultMaxAttempts,
		backoffBase:    defaultBackoffBase,
		backoffFactor:  defaultBackoffFactor,
		jitter:         defaultJitter,
		concurrency:    defaultConcurrency,
		perFileTimeout: defaultPerFileTimeout,
		globalTimeout:  defaultGlobalTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// newTransport construit un Transport explicite plutôt que de s'appuyer sur
// http.DefaultTransport : les délais par défaut de net/http sont trop
// permissifs pour un client qui doit respecter un budget de temps strict
// (docs/09-pipeline-mise-a-jour.md §3.1). TLSClientConfig n'est
// délibérément jamais renseigné avec InsecureSkipVerify : la vérification
// stricte du certificat de l'ANSM reste la configuration par défaut de Go.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   defaultConcurrency,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// defaultJitter ajoute un aléa uniforme dans [0, base/2] au repli
// exponentiel, pour éviter que plusieurs instances retentent exactement en
// même temps (courtoisie envers l'ANSM, même en cas d'incident).
// math/rand/v2 est sûr pour un usage concurrent sans verrou explicite,
// contrairement à un générateur math/rand non partagé.
func defaultJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	//nolint:gosec // G404: l'aléa d'un backoff n'a aucune exigence cryptographique ; il sert seulement à désynchroniser les instances, et crypto/rand y coûterait un appel système par tentative.
	return rand.N(base/2 + 1)
}

// FetchAll télécharge tous les fichiers demandés en parallèle, borné à
// c.concurrency connexions simultanées (errgroup.SetLimit). Une seule
// erreur, sur un seul fichier, annule le contexte dérivé et donc toutes les
// tentatives en cours sur les autres fichiers : neuf fichiers sur dix
// produiraient un jeu de données incohérent, pire qu'un jeu périmé
// (docs/09-pipeline-mise-a-jour.md §3.1, exigence E3).
func (c *Client) FetchAll(ctx context.Context, files []FileDescriptor) (DatasetResult, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, c.globalTimeout)
	defer cancel()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.concurrency)

	results := make([]FileResult, len(files))
	for i, f := range files {
		g.Go(func() error {
			r, err := c.fetchFile(gctx, f)
			if err != nil {
				return err
			}
			results[i] = r
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return DatasetResult{}, err
	}

	return DatasetResult{
		Files:    results,
		Hash:     datasetHash(results),
		Duration: time.Since(start),
	}, nil
}

// fetchFile télécharge un fichier avec tentatives et repli exponentiel. Le
// contexte est borné au délai par fichier, et respecté pendant l'attente de
// repli via un select sur ctx.Done() et un minuteur — jamais un time.Sleep
// nu, qui ignorerait une annulation externe (docs/09-pipeline-mise-a-jour.md
// §3.1).
func (c *Client) fetchFile(ctx context.Context, f FileDescriptor) (FileResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.perFileTimeout)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		result, retryable, retryAfter, err := c.attemptFetch(ctx, f)
		if err == nil {
			result.Attempts = attempt
			return result, nil
		}
		lastErr = err

		if !retryable || attempt == c.maxAttempts {
			break
		}

		wait := c.backoffDuration(attempt)
		if retryAfter > 0 && retryAfter <= maxReasonableRetryAfter {
			wait = retryAfter
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return FileResult{}, fmt.Errorf(
				"téléchargement de %s annulé pendant l'attente avant nouvelle tentative : %w", f.Name, ctx.Err(),
			)
		case <-timer.C:
		}
	}

	return FileResult{}, fmt.Errorf(
		"téléchargement de %s échoué après %d tentative(s) : %w", f.Name, c.maxAttempts, lastErr,
	)
}

// backoffDuration calcule le repli exponentiel avant la tentative suivant
// `attempt` (1-indexé) : base * facteur^(attempt-1), plus l'aléa injecté.
// Avec les valeurs par défaut (2s, facteur 4), la séquence est 2s, 8s, 32s,
// conforme à docs/09-pipeline-mise-a-jour.md §3.1.
func (c *Client) backoffDuration(attempt int) time.Duration {
	d := c.backoffBase
	for i := 1; i < attempt; i++ {
		d *= time.Duration(c.backoffFactor)
	}
	return d + c.jitter(d)
}

// attemptFetch effectue une unique tentative HTTP GET sur f. Elle retourne
// si l'échec est réessayable, et la durée d'attente demandée par un éventuel
// en-tête Retry-After.
//
// Seules les erreurs réseau, les 429 et les 5xx sont réessayables. Un 404
// ou un 400 est une erreur définitive de l'ANSM (fichier renommé, URL
// erronée) : la réessayer trois fois ne produirait que du bruit.
func (c *Client) attemptFetch(ctx context.Context, f FileDescriptor) (result FileResult, retryable bool, retryAfter time.Duration, err error) {
	start := time.Now()

	reqURL := c.baseURL + "/" + f.Path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return FileResult{}, false, 0, fmt.Errorf("construction de la requête pour %s : %w", f.Name, err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/plain, application/octet-stream")

	//nolint:bodyclose // fermé par `defer drainAndClose(resp.Body)` ci-dessous ; bodyclose ne suit pas la fermeture déléguée à une fonction auxiliaire.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return FileResult{}, false, 0, fmt.Errorf("requête vers %s annulée : %w", f.Name, ctx.Err())
		}
		// Erreur réseau (DNS, TCP, TLS, timeout de lecture d'en-tête…) :
		// potentiellement transitoire, donc réessayable.
		return FileResult{}, true, 0, fmt.Errorf("requête vers %s échouée : %w", f.Name, err)
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var wait time.Duration
		retryableStatus := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retryableStatus {
			wait, _ = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return FileResult{}, retryableStatus, wait, fmt.Errorf(
			"réponse HTTP %d de l'ANSM pour %s", resp.StatusCode, f.Name,
		)
	}

	// Lecture bornée à maxFileBytes+1 : io.LimitReader ne produit pas
	// d'erreur au dépassement, il tronque silencieusement — d'où la marge
	// d'un octet, qui permet de distinguer « fichier exactement à la
	// limite » de « fichier plus gros que la limite » et de refuser
	// explicitement ce second cas plutôt que de servir un contenu tronqué
	// sans le dire (docs/06-securite.md §8).
	h := sha256.New()
	limited := io.LimitReader(resp.Body, c.maxFileBytes+1)
	buf, err := io.ReadAll(io.TeeReader(limited, h))
	if err != nil {
		return FileResult{}, true, 0, fmt.Errorf("lecture du corps de %s échouée : %w", f.Name, err)
	}
	if int64(len(buf)) > c.maxFileBytes {
		return FileResult{}, false, 0, fmt.Errorf(
			"fichier %s dépasse la taille maximale autorisée (%d octets) : téléchargement interrompu", f.Name, c.maxFileBytes,
		)
	}

	return FileResult{
		Name:     f.Name,
		Bytes:    buf,
		SHA256:   hex.EncodeToString(h.Sum(nil)),
		Duration: time.Since(start),
	}, false, 0, nil
}

// drainAndClose vide le corps de réponse (dans une limite raisonnable) puis
// le ferme, y compris sur une tentative ratée : c'est ce qui permet à
// net/http de réutiliser la connexion TCP/TLS sous-jacente pour la requête
// suivante. Une omission ici, même sur un chemin d'erreur, dégraderait
// silencieusement les performances de toutes les synchronisations
// suivantes (connexions non réutilisées).
func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	_ = body.Close()
}

// parseRetryAfter interprète l'en-tête Retry-After au format « nombre de
// secondes » ou date HTTP, conformément à la RFC 9110. Une valeur absente,
// négative ou invalide retourne (0, false) : l'appelant retombe alors sur
// le repli exponentiel normal.
func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0, false
		}
		return d, true
	}
	return 0, false
}

// datasetHash calcule le hash global du jeu de données : SHA-256 de la
// concaténation des empreintes SHA-256 de chaque fichier, triées par nom de
// fichier. Le tri est ce qui rend le résultat déterministe indépendamment
// de l'ordre de complétion du téléchargement parallèle
// (docs/09-pipeline-mise-a-jour.md §3.2).
func datasetHash(files []FileResult) string {
	sorted := make([]FileResult, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var sb strings.Builder
	for _, f := range sorted {
		sb.WriteString(f.SHA256)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// URL retourne l'URL absolue d'un descripteur pour une racine de
// téléchargement donnée. Exportée pour permettre à l'appelant (journal,
// diagnostic) d'afficher exactement l'adresse interrogée sans dupliquer la
// logique de construction du chemin.
func (f FileDescriptor) URL(base string) string {
	return strings.TrimSuffix(base, "/") + "/" + f.Path
}
