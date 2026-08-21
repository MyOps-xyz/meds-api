package bdpm

import (
	"sort"
	"sync"
)

// Motifs propres à T-09, complétant ceux de reasons.go, qui couvrent les
// défauts détectables ligne par ligne. Ceux-ci ne sont détectables qu'une
// fois tous les fichiers parsés, car ils portent sur des relations entre
// fichiers.
const (
	// ReasonCISOrphelin signale une ligne dont le CIS n'existe pas dans
	// CIS_bdpm.txt (piège P6, docs/01-analyse-source-bdpm.md).
	//
	// C'est un motif *hors périmètre*, jamais un rejet : la ligne décrit une
	// spécialité absente du référentiel courant — typiquement un médicament
	// retiré du marché dont l'avis HAS ou l'appartenance à un groupe
	// générique subsiste dans les fichiers historiques. Elle est
	// inatteignable par l'API, dont tous les accès partent d'un CIS, mais
	// elle n'est pas malformée pour autant.
	//
	// Mesure du 15/08/2026 sur la source réelle : 8 831 lignes, dont 4 185
	// dans CIS_HAS_SMR_bdpm.txt, 2 513 dans CIS_GENER_bdpm.txt, 2 115 dans
	// CIS_HAS_ASMR_bdpm.txt, 12 dans CIS_CIP_Dispo_Spec.txt, 4 dans
	// CIS_CIP_bdpm.txt et 2 dans CIS_MITM.txt. Seules les quatre dernières
	// étaient documentées ; les compter comme des rejets porterait le taux à
	// 5,8 % et condamnerait toute ingestion (ADR 0007).
	ReasonCISOrphelin = "cis_orphelin"
	// ReasonCIP13Orphelin signale une rupture rattachée à un CIP13 absent
	// de CIS_CIP_bdpm.txt. La ligne est conservée sans son CIP13 plutôt que
	// rejetée : l'information de rupture au niveau de la spécialité reste
	// exacte et utile. Hors périmètre également.
	ReasonCIP13Orphelin = "cip13_orphelin"
	// ReasonCIP13Duplique signale deux présentations portant le même CIP13.
	// Aucune occurrence mesurée, mais le contrôle est nécessaire : le CIP13
	// est une clé d'index d'accès direct (T-16), un doublon y serait une
	// donnée silencieusement perdue.
	ReasonCIP13Duplique = "cip13_duplique"
	// ReasonCISDuplique signale deux spécialités portant le même CIS.
	ReasonCISDuplique = "cis_duplique"
)

// QuarantineEntry est une ligne écartée, conservée pour l'audit. Raw est
// tronquée à MaxQuarantineRawLen : un rapport d'ingestion doit rester
// lisible et borné en mémoire même si un fichier amont part en vrille.
type QuarantineEntry struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
	Raw    string `json:"raw,omitempty"`
}

const (
	// MaxQuarantineRawLen borne la longueur de la ligne brute conservée.
	MaxQuarantineRawLen = 512
	// MaxQuarantineEntries borne le nombre d'entrées détaillées retenues.
	// Au-delà, seuls les compteurs continuent d'être incrémentés : le
	// contrôle de vraisemblance (taux de rejet) a de toute façon déjà
	// condamné l'ingestion bien avant ce seuil, et un fichier amont
	// intégralement corrompu ne doit pas pouvoir saturer la mémoire.
	MaxQuarantineEntries = 1000
)

// Quarantine collecte les lignes écartées, par fichier et par motif.
//
// Elle distingue deux catégories, dont la confusion est ce qui rendait le
// garde-fou de vraisemblance inopérant (ADR 0007) :
//
//   - Le **rejet** : la ligne est malformée — colonnes manquantes, date
//     illisible, prix non convertible. C'est un défaut de qualité, et son
//     taux est ce que surveille MEDS_MAX_REJECT_RATIO pour détecter un
//     changement de format amont.
//   - Le **hors périmètre** : la ligne est parfaitement formée mais réfère
//     une entité absente du référentiel courant. C'est une propriété
//     structurelle et stable de la source, pas un incident. La compter comme
//     un rejet ferait échouer toute ingestion sur des données saines.
//
// Toutes ses méthodes sont sûres en concurrence : les dix parsers tournent
// en parallèle et partagent une même instance.
type Quarantine struct {
	mu        sync.Mutex
	entries   []QuarantineEntry
	byFile    map[string]int
	byReason  map[string]int
	total     int
	truncated int

	scopeByFile   map[string]int
	scopeByReason map[string]int
	scopeTotal    int

	// unknown compte les valeurs d'énumération jamais observées, par
	// (fichier, champ, valeur). Ce ne sont pas des rejets : la ligne est
	// conservée. Elles alimentent les avertissements du manifest, pour que
	// l'ajout d'une modalité par l'ANSM soit visible sans être bloquant.
	unknown map[unknownKey]int
}

type unknownKey struct{ file, field, value string }

// NewQuarantine construit un collecteur vide.
func NewQuarantine() *Quarantine {
	return &Quarantine{
		byFile:        make(map[string]int),
		byReason:      make(map[string]int),
		scopeByFile:   make(map[string]int),
		scopeByReason: make(map[string]int),
		unknown:       make(map[unknownKey]int),
	}
}

// Add enregistre une ligne écartée. Sa signature est celle de
// QuarantineFunc, de sorte que (*Quarantine).Add se passe directement aux
// parsers de T-08.
func (q *Quarantine) Add(file string, line int, reason string, raw string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.total++
	q.byFile[file]++
	q.byReason[reason]++

	if len(q.entries) >= MaxQuarantineEntries {
		q.truncated++
		return
	}
	if len(raw) > MaxQuarantineRawLen {
		raw = raw[:MaxQuarantineRawLen]
	}
	q.entries = append(q.entries, QuarantineEntry{
		File: file, Line: line, Reason: reason, Raw: raw,
	})
}

// AddOutOfScope enregistre une ligne écartée pour cause de référent absent
// du périmètre courant. Elle n'entre pas dans le taux de rejet.
//
// Les entrées détaillées ne sont pas conservées : à 8 831 lignes par
// ingestion, ce sont les compteurs par fichier et par motif qui portent
// l'information utile, et un échantillon exhaustif ne ferait que gonfler le
// manifest sans rien apprendre à l'exploitant.
func (q *Quarantine) AddOutOfScope(file string, _ int, reason string, _ string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.scopeTotal++
	q.scopeByFile[file]++
	q.scopeByReason[reason]++
}

// OutOfScopeTotal est le nombre de lignes écartées comme hors périmètre.
func (q *Quarantine) OutOfScopeTotal() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.scopeTotal
}

// OutOfScopeByFile restitue les compteurs hors périmètre par fichier.
func (q *Quarantine) OutOfScopeByFile() map[string]int { return q.copyCounts(q.scopeByFile) }

// OutOfScopeByReason restitue les compteurs hors périmètre par motif.
func (q *Quarantine) OutOfScopeByReason() map[string]int { return q.copyCounts(q.scopeByReason) }

// AddUnknown enregistre une valeur d'énumération inattendue. Sa signature
// est celle de UnknownValueFunc.
func (q *Quarantine) AddUnknown(file, field, value string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.unknown[unknownKey{file, field, value}]++
}

// Total est le nombre de lignes écartées, troncature comprise.
func (q *Quarantine) Total() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.total
}

// Entries restitue les lignes écartées retenues, triées par fichier puis
// par numéro de ligne — l'ordre de collecte dépend de l'ordonnancement des
// parsers concurrents et ne serait pas reproductible.
func (q *Quarantine) Entries() []QuarantineEntry {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]QuarantineEntry, len(q.entries))
	copy(out, q.entries)
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// ByFile et ByReason restituent les compteurs. Les cartes retournées sont
// des copies : l'appelant peut les conserver dans un manifest sans risque
// de lecture concurrente pendant qu'une ingestion se poursuit.
func (q *Quarantine) ByFile() map[string]int { return q.copyCounts(q.byFile) }

// ByReason restitue le nombre de lignes écartées par motif.
func (q *Quarantine) ByReason() map[string]int { return q.copyCounts(q.byReason) }

func (q *Quarantine) copyCounts(src map[string]int) map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// UnknownValue décrit une modalité d'énumération inconnue et son nombre
// d'occurrences.
type UnknownValue struct {
	File  string `json:"file"`
	Field string `json:"field"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

// UnknownValues restitue les modalités inattendues, triées, pour un rendu
// reproductible dans le manifest.
func (q *Quarantine) UnknownValues() []UnknownValue {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]UnknownValue, 0, len(q.unknown))
	for k, n := range q.unknown {
		out = append(out, UnknownValue{File: k.file, Field: k.field, Value: k.value, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Value < out[j].Value
	})
	return out
}
