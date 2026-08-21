package bdpm

import (
	"fmt"
	"sort"
)

// ValidationOptions paramètre les garde-fous de vraisemblance
// (docs/09-pipeline-mise-a-jour.md §3.6).
type ValidationOptions struct {
	// MaxRejectRatio est le taux de rejet au-delà duquel l'ingestion
	// échoue. Correspond à MEDS_MAX_REJECT_RATIO (défaut 0,01).
	MaxRejectRatio float64
	// MinSpecialites est le plancher du nombre de spécialités. 15 857
	// mesurées le 14/08/2026 ; un effondrement sous 10 000 signale un
	// changement de format amont, pas une actualité pharmaceutique.
	MinSpecialites int
	// MaxVariationRatio borne l'écart relatif toléré vis-à-vis du snapshot
	// précédent, par entité (défaut 0,20).
	MaxVariationRatio float64
	// MaxOutOfScopeRatio borne la part de lignes hors périmètre. Ce n'est
	// pas un contrôle de qualité mais un contrôle de cohérence : 5,8 %
	// mesurés le 15/08/2026, valeur stable d'une ingestion à l'autre. Une
	// envolée signalerait que CIS_bdpm.txt a été tronqué en amont — la
	// panne exacte que le taux de rejet ne peut plus détecter depuis que
	// les deux catégories sont séparées (ADR 0007). Défaut 0,20.
	MaxOutOfScopeRatio float64
	// PreviousCounts sont les compteurs du snapshot en service. Nil lors de
	// la toute première ingestion : le contrôle de variation est alors
	// inapplicable et silencieusement ignoré, plutôt que de faire échouer
	// un démarrage à froid.
	PreviousCounts map[string]int
}

// DefaultValidationOptions retourne les seuils documentés.
func DefaultValidationOptions() ValidationOptions {
	return ValidationOptions{
		MaxRejectRatio:     0.01,
		MinSpecialites:     10000,
		MaxVariationRatio:  0.20,
		MaxOutOfScopeRatio: 0.20,
	}
}

// ValidationError décrit un garde-fou franchi. C'est une erreur terminale
// pour l'ingestion : le Store en service n'est pas touché (exigence E3).
type ValidationError struct {
	Check   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("contrôle de vraisemblance « %s » : %s", e.Check, e.Message)
}

// Identifiants des contrôles, stables pour l'étiquetage des métriques et
// des alertes.
const (
	CheckFichierVide       = "fichier_vide"
	CheckTauxRejet         = "taux_rejet"
	CheckPlancherSpecs     = "plancher_specialites"
	CheckVariationCompteur = "variation_compteur"
	CheckHorsPerimetre     = "hors_perimetre"
)

// CheckReferentialIntegrity applique le deuxième niveau de validation :
// tout CIS référencé doit exister dans CIS_bdpm.txt (piège P6), et tout
// CIP13 référencé par une rupture doit exister dans CIS_CIP_bdpm.txt.
//
// Les lignes orphelines sont écartées comme **hors périmètre**, catégorie
// distincte du rejet et exclue du taux surveillé : elles réfèrent des
// spécialités absentes du référentiel courant, ce qui est une propriété
// structurelle de la source et non un défaut de qualité. Mesure du
// 15/08/2026 : 8 831 lignes, soit 5,8 % du jeu — les compter comme des
// rejets condamnerait chaque ingestion (ADR 0007).
//
// Les doublons de clé primaire (CIS, CIP13) sont, eux, de véritables
// rejets : ce sont des collisions d'index, dont la conséquence serait une
// donnée silencieusement perdue à la construction du Store (T-16).
func CheckReferentialIntegrity(d *Dataset, q *Quarantine) {
	knownCIS := make(map[string]struct{}, len(d.Specialites))
	d.Specialites = filterInPlace(d.Specialites, func(i int, s Specialite) bool {
		if _, dup := knownCIS[s.CIS]; dup {
			q.Add(FileSpecialites, i+1, ReasonCISDuplique, s.CIS)
			return false
		}
		knownCIS[s.CIS] = struct{}{}
		return true
	})

	knownCIP13 := make(map[string]struct{}, len(d.Presentations))
	d.Presentations = filterInPlace(d.Presentations, func(i int, p Presentation) bool {
		if _, ok := knownCIS[p.CIS]; !ok {
			q.AddOutOfScope(FilePresentations, i+1, ReasonCISOrphelin, p.CIP13+"\t"+p.CIS)
			return false
		}
		if _, dup := knownCIP13[p.CIP13]; dup {
			q.Add(FilePresentations, i+1, ReasonCIP13Duplique, p.CIP13)
			return false
		}
		knownCIP13[p.CIP13] = struct{}{}
		return true
	})

	d.Composants = filterInPlace(d.Composants, func(i int, c Composant) bool {
		return keepOrQuarantine(q, FileComposants, i, knownCIS, c.CIS, c.CIS)
	})
	d.AvisSMR = filterInPlace(d.AvisSMR, func(i int, a AvisSMR) bool {
		return keepOrQuarantine(q, FileAvisSMR, i, knownCIS, a.CIS, a.CIS)
	})
	d.AvisASMR = filterInPlace(d.AvisASMR, func(i int, a AvisASMR) bool {
		return keepOrQuarantine(q, FileAvisASMR, i, knownCIS, a.CIS, a.CIS)
	})
	d.Appartenances = filterInPlace(d.Appartenances, func(i int, a GroupeAppartenance) bool {
		return keepOrQuarantine(q, FileGroupes, i, knownCIS, a.CIS, a.GroupeID+"\t"+a.CIS)
	})
	d.Conditions = filterInPlace(d.Conditions, func(i int, c Condition) bool {
		return keepOrQuarantine(q, FileConditions, i, knownCIS, c.CIS, c.CIS)
	})
	d.MITM = filterInPlace(d.MITM, func(i int, m InfoMITM) bool {
		return keepOrQuarantine(q, FileMITM, i, knownCIS, m.CIS, m.CIS)
	})

	// Les ruptures sont le seul cas où un référent manquant ne condamne pas
	// la ligne : un CIP13 inconnu est neutralisé, mais la rupture reste
	// rattachée à sa spécialité. Perdre l'information « ce médicament est en
	// rupture » parce que le conditionnement exact est introuvable serait un
	// mauvais arbitrage sur une donnée de santé.
	d.Ruptures = filterInPlace(d.Ruptures, func(i int, r Rupture) bool {
		if _, ok := knownCIS[r.CIS]; !ok {
			q.AddOutOfScope(FileRuptures, i+1, ReasonCISOrphelin, r.CIS)
			return false
		}
		if r.CIP13 != nil {
			if _, ok := knownCIP13[*r.CIP13]; !ok {
				q.AddOutOfScope(FileRuptures, i+1, ReasonCIP13Orphelin, r.CIS+"\t"+*r.CIP13)
				d.Ruptures[i].CIP13 = nil
			}
		}
		return true
	})
}

func keepOrQuarantine(q *Quarantine, file string, i int, known map[string]struct{}, cis, raw string) bool {
	if _, ok := known[cis]; ok {
		return true
	}
	q.AddOutOfScope(file, i+1, ReasonCISOrphelin, raw)
	return false
}

// filterInPlace réécrit s en ne conservant que les éléments pour lesquels
// keep répond vrai, sans allocation. L'index passé à keep est celui de
// l'élément dans la slice d'origine, ce qui donne un numéro de ligne
// approché mais exploitable dans la quarantaine — le numéro exact est déjà
// perdu à ce stade, les parsers ayant écarté leurs propres lignes.
func filterInPlace[T any](s []T, keep func(i int, v T) bool) []T {
	n := 0
	for i := range s {
		if keep(i, s[i]) {
			s[n] = s[i]
			n++
		}
	}
	return s[:n]
}

// CheckPlausibility applique le troisième niveau : les garde-fous globaux
// contre un changement de format amont.
//
// L'ordre des contrôles va du plus explicite au plus général : un fichier
// vide donne un diagnostic bien plus utile qu'un taux de rejet à 100 %.
func CheckPlausibility(d *Dataset, q *Quarantine, opts ValidationOptions) error {
	empty := []string{}
	for name, n := range map[string]int{
		FileSpecialites:   len(d.Specialites),
		FilePresentations: len(d.Presentations),
		FileComposants:    len(d.Composants),
	} {
		if n == 0 {
			empty = append(empty, name)
		}
	}
	if len(empty) > 0 {
		sort.Strings(empty)
		return &ValidationError{
			Check:   CheckFichierVide,
			Message: fmt.Sprintf("aucun enregistrement retenu pour %v", empty),
		}
	}

	kept := d.TotalRecords()
	rejected := q.Total()
	if total := kept + rejected; total > 0 {
		ratio := float64(rejected) / float64(total)
		if ratio > opts.MaxRejectRatio {
			return &ValidationError{
				Check: CheckTauxRejet,
				Message: fmt.Sprintf("%.4f%% de lignes rejetées (%d sur %d), seuil %.4f%%",
					ratio*100, rejected, total, opts.MaxRejectRatio*100),
			}
		}
	}

	if opts.MaxOutOfScopeRatio > 0 {
		scope := q.OutOfScopeTotal()
		if total := d.TotalRecords() + scope; total > 0 {
			ratio := float64(scope) / float64(total)
			if ratio > opts.MaxOutOfScopeRatio {
				return &ValidationError{
					Check: CheckHorsPerimetre,
					Message: fmt.Sprintf("%.2f%% de lignes hors périmètre (%d sur %d), seuil %.0f%% — CIS_bdpm.txt est probablement tronqué",
						ratio*100, scope, total, opts.MaxOutOfScopeRatio*100),
				}
			}
		}
	}

	if len(d.Specialites) < opts.MinSpecialites {
		return &ValidationError{
			Check: CheckPlancherSpecs,
			Message: fmt.Sprintf("%d spécialités, plancher %d",
				len(d.Specialites), opts.MinSpecialites),
		}
	}

	if opts.PreviousCounts != nil && opts.MaxVariationRatio > 0 {
		counts := d.Counts()
		names := make([]string, 0, len(counts))
		for name := range counts {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			prev, ok := opts.PreviousCounts[name]
			if !ok || prev == 0 {
				continue
			}
			variation := float64(counts[name]-prev) / float64(prev)
			if variation < 0 {
				variation = -variation
			}
			if variation > opts.MaxVariationRatio {
				return &ValidationError{
					Check: CheckVariationCompteur,
					Message: fmt.Sprintf("%s : %d → %d (%.1f%%), seuil ±%.0f%%",
						name, prev, counts[name], variation*100, opts.MaxVariationRatio*100),
				}
			}
		}
	}

	return nil
}

// RejectRatio est le taux de rejet effectif de l'ingestion, tel qu'il est
// inscrit au manifest. Les lignes hors périmètre en sont exclues, au
// numérateur comme au dénominateur : ce taux mesure la malformation, pas la
// couverture du référentiel.
func RejectRatio(d *Dataset, q *Quarantine) float64 {
	total := d.TotalRecords() + q.Total()
	if total == 0 {
		return 0
	}
	return float64(q.Total()) / float64(total)
}

// OutOfScopeRatio est la part de lignes écartées faute de référent dans le
// périmètre courant.
func OutOfScopeRatio(d *Dataset, q *Quarantine) float64 {
	total := d.TotalRecords() + q.OutOfScopeTotal()
	if total == 0 {
		return 0
	}
	return float64(q.OutOfScopeTotal()) / float64(total)
}

// Warnings produit les avertissements lisibles du manifest : ce qui mérite
// l'attention d'un exploitant sans avoir condamné l'ingestion.
func Warnings(q *Quarantine) []string {
	var out []string
	byReason := q.ByReason()
	reasons := make([]string, 0, len(byReason))
	for r := range byReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		out = append(out, fmt.Sprintf("%d ligne(s) rejetée(s) pour motif « %s »", byReason[r], r))
	}

	scope := q.OutOfScopeByReason()
	scopeReasons := make([]string, 0, len(scope))
	for r := range scope {
		scopeReasons = append(scopeReasons, r)
	}
	sort.Strings(scopeReasons)
	for _, r := range scopeReasons {
		out = append(out, fmt.Sprintf(
			"%d ligne(s) hors périmètre pour motif « %s » — non servies, non comptées comme rejets",
			scope[r], r))
	}
	for _, u := range q.UnknownValues() {
		out = append(out, fmt.Sprintf(
			"valeur inconnue dans %s, champ %s : %q (%d occurrence(s)) — conservée telle quelle",
			u.File, u.Field, u.Value, u.Count))
	}
	return out
}
