// Package snapshot lit et écrit les snapshots NDJSON du jeu de données.
//
// Un snapshot est un répertoire immuable nommé par le hash global de ses
// fichiers sources, contenant une entité par fichier NDJSON et un
// manifest.json. La garantie centrale est qu'un répertoire portant un
// manifest est complet et exploitable : l'écriture se fait dans un
// répertoire temporaire, renommé d'un bloc, et le manifest est écrit en
// dernier (docs/09-pipeline-mise-a-jour.md §3.7).
package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

// Noms de fichiers du snapshot. Ce sont des constantes de contrat : le
// manifest les référence, et le chargement du Store (T-17) s'y fie.
const (
	ManifestFile      = "manifest.json"
	SpecialitesFile   = "specialites.ndjson"
	PresentationsFile = "presentations.ndjson"
	ComposantsFile    = "composants.ndjson"
	SubstancesFile    = "substances.ndjson"
	GroupesFile       = "groupes_generiques.ndjson"
	AvisSMRFile       = "avis_smr.ndjson"
	AvisASMRFile      = "avis_asmr.ndjson"
	ConditionsFile    = "conditions.ndjson"
	RupturesFile      = "ruptures.ndjson"
	MITMFile          = "mitm.ndjson"

	// CurrentLink est le lien symbolique vers le snapshot en service.
	CurrentLink = "current"
	// SnapshotsDir est le sous-répertoire de MEDS_DATA_DIR abritant les
	// snapshots.
	SnapshotsDir = "snapshots"
	// tmpPrefix préfixe les répertoires d'écriture en cours. Les résidus
	// sont purgés au démarrage suivant.
	tmpPrefix = ".tmp-"
)

// QuarantineSummary résume les lignes rejetées pour malformation
// (docs/03-modele-de-donnees.md §3.8).
type QuarantineSummary struct {
	Total       int            `json:"total"`
	ByFile      map[string]int `json:"by_file"`
	ByReason    map[string]int `json:"by_reason"`
	RejectRatio float64        `json:"reject_ratio"`
}

// OutOfScopeSummary résume les lignes écartées faute de référent dans le
// périmètre courant. Catégorie distincte du rejet, exposée séparément sur
// /v1/dataset pour qu'un intégrateur puisse constater qu'une partie des
// fichiers HAS et génériques concerne des spécialités retirées du marché,
// sans y lire un défaut de qualité (ADR 0007).
type OutOfScopeSummary struct {
	Total    int            `json:"total"`
	ByFile   map[string]int `json:"by_file"`
	ByReason map[string]int `json:"by_reason"`
	Ratio    float64        `json:"ratio"`
}

// Manifest décrit un snapshot. Sa présence dans un répertoire atteste que
// le snapshot est complet.
type Manifest struct {
	// Version est l'horodatage RFC 3339 de génération, en UTC. C'est la
	// version publique du jeu de données, celle qu'expose /v1/dataset.
	Version string `json:"version"`
	// Hash est le hash global des fichiers sources : SHA-256 des SHA-256
	// triés par nom. Il nomme le répertoire, alimente les ETag HTTP et sert
	// de graine aux curseurs de pagination.
	Hash string `json:"hash"`
	// PreviousHash est le hash du snapshot précédent, vide à la première
	// ingestion.
	PreviousHash string `json:"previous_hash,omitempty"`

	GeneratedAt string `json:"generated_at"`
	Generator   string `json:"generator"`

	SourceFiles []bdpm.SourceFile `json:"source_files"`
	Counts      map[string]int    `json:"counts"`
	Quarantine  QuarantineSummary `json:"quarantine"`
	OutOfScope  OutOfScopeSummary `json:"out_of_scope"`
	Warnings    []string          `json:"warnings,omitempty"`

	// UnknownValues liste les modalités d'énumération jamais observées
	// jusqu'ici. Elles ne sont pas des rejets : la ligne est conservée.
	UnknownValues []bdpm.UnknownValue `json:"unknown_values,omitempty"`
}

// ReadManifest charge le manifest d'un répertoire de snapshot.
func ReadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("lecture du manifest : %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest illisible dans %s : %w", dir, err)
	}
	if m.Hash == "" {
		return nil, fmt.Errorf("manifest sans hash dans %s", dir)
	}
	return &m, nil
}
