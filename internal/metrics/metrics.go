// Package metrics expose les métriques Prometheus de
// docs/10-observabilite.md §3.
//
// Toutes les étiquettes sont à **cardinalité bornée**. C'est la contrainte
// qui structure ce paquet : une étiquette non bornée — un code CIS, une
// requête de recherche, une adresse IP — crée une série temporelle par
// valeur distincte et sature le stockage de Prometheus. C'est l'erreur
// d'instrumentation la plus courante et la plus coûteuse.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Buckets adaptés aux budgets de docs/07-performance.md §2.1.
//
// Les bornes par défaut de Prometheus démarrent à 5 ms : sur une API dont le
// p99 visé est de 200 µs, elles n'auraient aucune résolution utile — tout
// tomberait dans le premier seau.
//
// **Chaque budget documenté est une borne exacte.** 200 µs, 1 ms et 3 ms
// figurent tels quels dans la liste, sans quoi le budget serait tout
// simplement invérifiable : la liste de docs/10-observabilite.md §3.1
// proposait 250 µs et 5 ms, qui encadrent les budgets sans les atteindre, et
// un p99 « dans le seau (0,1 ms – 0,25 ms] » ne permet pas de trancher sur un
// seuil de 0,2 ms. Constaté en mesurant, le 15/08/2026.
var latencyBuckets = []float64{
	0.0001, 0.0002, 0.00025, 0.0005, 0.001, 0.0025, 0.003, 0.005, 0.01, 0.025, 0.1, 0.5, 1,
}

var sizeBuckets = prometheus.ExponentialBuckets(256, 4, 8) // 256 o → 4 Mio

// Metrics regroupe tous les collecteurs du service.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests   *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
	httpSize       *prometheus.HistogramVec
	httpInFlight   prometheus.Gauge
	httpPanics     *prometheus.CounterVec
	authFailures   prometheus.Counter
	rateLimitedVec *prometheus.CounterVec

	datasetAge        prometheus.GaugeFunc
	datasetInfo       *prometheus.GaugeVec
	datasetRecords    *prometheus.GaugeVec
	datasetQuarantine *prometheus.GaugeVec
	datasetOutOfScope *prometheus.GaugeVec
	storeBuild        prometheus.Histogram
	storeSwaps        prometheus.Counter

	syncAttempts   *prometheus.CounterVec
	syncDuration   prometheus.Histogram
	syncLastOK     prometheus.Gauge
	syncInProgress prometheus.Gauge

	searchDuration *prometheus.HistogramVec
	searchResults  prometheus.Histogram
	searchFallback prometheus.Counter

	// ageSource fournit l'horodatage de génération du jeu de données servi.
	// La fraîcheur est calculée **à la collecte** et non stockée : une jauge
	// figée vieillirait faux entre deux synchronisations.
	ageSource func() (time.Time, bool)
}

// New construit et enregistre tous les collecteurs.
//
// ageSource peut être nil ; il est branché plus tard par SetAgeSource, la
// jauge de fraîcheur ayant besoin du Holder qui n'existe pas encore au
// moment où les métriques sont créées.
func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}

	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meds_http_requests_total",
		Help: "Nombre de requêtes HTTP servies.",
	}, []string{"route", "method", "status"})

	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "meds_http_request_duration_seconds",
		Help:    "Durée de traitement des requêtes HTTP.",
		Buckets: latencyBuckets,
	}, []string{"route", "method"})

	m.httpSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "meds_http_response_size_bytes",
		Help:    "Taille des réponses HTTP.",
		Buckets: sizeBuckets,
	}, []string{"route"})

	m.httpInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "meds_http_in_flight_requests",
		Help: "Requêtes en cours de traitement.",
	})

	m.httpPanics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meds_http_panics_total",
		Help: "Paniques récupérées dans les handlers.",
	}, []string{"route"})

	m.authFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "meds_auth_failures_total",
		Help: "Tentatives d'accès avec une clé absente ou invalide.",
	})

	m.rateLimitedVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meds_ratelimit_rejected_total",
		Help: "Requêtes refusées par la limitation de débit.",
	}, []string{"scope"})

	m.datasetInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "meds_dataset_info",
		Help: "Identification du jeu de données servi (valeur toujours 1).",
	}, []string{"version", "hash"})

	m.datasetRecords = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "meds_dataset_records",
		Help: "Nombre d'enregistrements par entité.",
	}, []string{"entity"})

	m.datasetQuarantine = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "meds_dataset_quarantine_records",
		Help: "Lignes rejetées pour malformation, par fichier et par motif.",
	}, []string{"file", "reason"})

	m.datasetOutOfScope = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "meds_dataset_out_of_scope_records",
		Help: "Lignes écartées faute de référent dans le périmètre courant (ADR 0007).",
	}, []string{"file", "reason"})

	m.storeBuild = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "meds_store_build_duration_seconds",
		Help:    "Durée de construction du Store en mémoire.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

	m.storeSwaps = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "meds_store_swaps_total",
		Help: "Nombre de bascules de Store.",
	})

	m.syncAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meds_sync_attempts_total",
		Help: "Synchronisations, par issue.",
	}, []string{"result"})

	m.syncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "meds_sync_duration_seconds",
		Help:    "Durée d'une synchronisation complète.",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
	})

	m.syncLastOK = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "meds_sync_last_success_timestamp",
		Help: "Horodatage Unix de la dernière synchronisation réussie.",
	})

	m.syncInProgress = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "meds_sync_in_progress",
		Help: "1 si une synchronisation est en cours.",
	})

	m.searchDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "meds_search_duration_seconds",
		Help:    "Durée d'une recherche, par mode.",
		Buckets: latencyBuckets,
	}, []string{"mode"})

	m.searchResults = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "meds_search_results_count",
		Help:    "Nombre de résultats d'une recherche.",
		Buckets: []float64{0, 1, 5, 10, 50, 100, 500, 1000, 5000},
	})

	m.searchFallback = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "meds_search_fallback_total",
		Help: "Recherches ayant déclenché le repli trigramme.",
	})

	// La fraîcheur est la métrique la plus importante du service : elle
	// répond seule à « la donnée servie est-elle à jour ? ».
	m.datasetAge = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "meds_dataset_age_seconds",
		Help: "Âge du jeu de données servi, en secondes.",
	}, func() float64 {
		if m.ageSource == nil {
			return -1
		}
		t, ok := m.ageSource()
		if !ok {
			// -1 distingue « aucune donnée servie » de « donnée fraîche »,
			// que 0 confondrait.
			return -1
		}
		return time.Since(t).Seconds()
	})

	m.registry.MustRegister(
		m.httpRequests, m.httpDuration, m.httpSize, m.httpInFlight, m.httpPanics,
		m.authFailures, m.rateLimitedVec,
		m.datasetAge, m.datasetInfo, m.datasetRecords, m.datasetQuarantine,
		m.datasetOutOfScope, m.storeBuild, m.storeSwaps,
		m.syncAttempts, m.syncDuration, m.syncLastOK, m.syncInProgress,
		m.searchDuration, m.searchResults, m.searchFallback,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Les combinaisons d'étiquettes connues sont initialisées à zéro.
	//
	// Un CounterVec jamais incrémenté n'est pas exposé du tout : la série
	// n'apparaîtrait qu'au premier incident, c'est-à-dire au pire moment.
	// `rate()` sur une série absente ne rend rien, une alerte bâtie dessus
	// ne se déclencherait donc jamais, et un tableau de bord afficherait un
	// trou plutôt qu'un zéro. Les déclarer à l'avance rend l'absence
	// d'incident aussi visible que sa présence.
	for _, scope := range []string{"key", "ip"} {
		m.rateLimitedVec.WithLabelValues(scope)
	}
	for _, result := range []string{"success", "unchanged", "failure"} {
		m.syncAttempts.WithLabelValues(result)
	}
	for _, mode := range []string{"exact", "trigram"} {
		m.searchDuration.WithLabelValues(mode)
	}

	return m
}

// SetAgeSource branche la source de fraîcheur du jeu de données.
func (m *Metrics) SetAgeSource(f func() (time.Time, bool)) { m.ageSource = f }

// Handler sert l'exposition Prometheus.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// Une erreur de collecte ne doit pas faire échouer le scrape : mieux
		// vaut des métriques partielles qu'aucune métrique pendant un
		// incident, moment où elles sont le plus utiles.
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// Registry expose le registre, pour les tests.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// ObserveRequest enregistre une requête servie.
//
// route est le **patron** (`/v1/medicaments/{cis}`). Un patron vide — route
// inconnue — est remplacé par « other » plutôt que laissé vide : sans cela,
// un balayage d'URL aléatoires créerait autant de séries que d'URL tentées.
func (m *Metrics) ObserveRequest(route, method string, status int, d time.Duration, size int) {
	if route == "" {
		route = "other"
	}
	m.httpRequests.WithLabelValues(route, method, statusLabel(status)).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(d.Seconds())
	if size > 0 {
		m.httpSize.WithLabelValues(route).Observe(float64(size))
	}
	switch status {
	case http.StatusUnauthorized:
		m.authFailures.Inc()
	case http.StatusTooManyRequests:
		m.rateLimitedVec.WithLabelValues("key").Inc()
	}
}

// statusLabel réduit le statut à sa classe plus les codes remarquables.
//
// Étiqueter le code exact serait tentant mais inutilement large ; la classe
// suffit à calculer un taux d'erreur, et les quatre codes conservés en
// entier sont ceux sur lesquels on alerte spécifiquement.
func statusLabel(status int) string {
	switch status {
	case 401, 404, 429, 503:
		return strconv.Itoa(status)
	}
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

// IncInFlight et DecInFlight encadrent le traitement d'une requête.
func (m *Metrics) IncInFlight() { m.httpInFlight.Inc() }

// DecInFlight signale la fin du traitement d'une requête.
func (m *Metrics) DecInFlight() { m.httpInFlight.Dec() }

// ObservePanic enregistre une panique récupérée.
func (m *Metrics) ObservePanic(route string) {
	if route == "" {
		route = "other"
	}
	m.httpPanics.WithLabelValues(route).Inc()
}

// ObserveSearch enregistre une recherche.
func (m *Metrics) ObserveSearch(mode string, d time.Duration, results int, fallback bool) {
	m.searchDuration.WithLabelValues(mode).Observe(d.Seconds())
	m.searchResults.Observe(float64(results))
	if fallback {
		m.searchFallback.Inc()
	}
}

// SyncStarted signale le début d'une synchronisation.
func (m *Metrics) SyncStarted() { m.syncInProgress.Set(1) }

// SyncFinished enregistre l'issue d'une synchronisation.
//
// `unchanged` est distingué de `success` : la plupart des jours, l'issue
// normale est que la source n'a pas bougé. Confondre les deux rendrait
// impossible d'alerter sur une absence prolongée de mise à jour réelle, et
// compter `unchanged` comme un échec déclencherait des alertes permanentes.
func (m *Metrics) SyncFinished(result string, d time.Duration, at time.Time) {
	m.syncInProgress.Set(0)
	m.syncAttempts.WithLabelValues(result).Inc()
	m.syncDuration.Observe(d.Seconds())
	if result == "success" || result == "unchanged" {
		m.syncLastOK.Set(float64(at.Unix()))
	}
}

// DatasetSnapshot décrit le jeu de données publié, pour les jauges.
//
// C'est un **alias** vers une structure anonyme, et non un type défini :
// le paquet pipeline déclare le même alias de son côté, ce qui rend les deux
// types identiques et permet à *Metrics de satisfaire pipeline.Recorder sans
// que pipeline n'importe ni ce paquet ni Prometheus. La CLI bdpm-sync
// n'embarque ainsi aucune dépendance d'instrumentation.
//
// Quarantine et OutOfScope ont pour clé « fichier|motif ».
type DatasetSnapshot = struct {
	Version    string
	Hash       string
	Counts     map[string]int
	Quarantine map[string]int
	OutOfScope map[string]int
	BuildTime  time.Duration
}

// PublishDataset met à jour les jauges après une bascule de Store.
//
// Les jauges par entité sont **remises à zéro** avant réécriture : sans
// cela, une entité disparue de la source garderait éternellement sa dernière
// valeur, et un tableau de bord afficherait une donnée qui n'existe plus.
func (m *Metrics) PublishDataset(s DatasetSnapshot) {
	m.datasetInfo.Reset()
	m.datasetInfo.WithLabelValues(s.Version, s.Hash).Set(1)

	m.datasetRecords.Reset()
	for entity, n := range s.Counts {
		m.datasetRecords.WithLabelValues(entity).Set(float64(n))
	}

	m.datasetQuarantine.Reset()
	for key, n := range s.Quarantine {
		file, reason := splitKey(key)
		m.datasetQuarantine.WithLabelValues(file, reason).Set(float64(n))
	}

	m.datasetOutOfScope.Reset()
	for key, n := range s.OutOfScope {
		file, reason := splitKey(key)
		m.datasetOutOfScope.WithLabelValues(file, reason).Set(float64(n))
	}

	if s.BuildTime > 0 {
		m.storeBuild.Observe(s.BuildTime.Seconds())
	}
	m.storeSwaps.Inc()
}

func splitKey(key string) (file, reason string) {
	file, reason, _ = strings.Cut(key, "|")
	return file, reason
}
