// Package config charge et valide la configuration du service depuis
// l'environnement (12-factor). Voir docs/02-architecture.md §7.
package config

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CronSchedule représente une planification cron classique à cinq champs
// (minute heure jour-du-mois mois jour-de-semaine), déjà résolue en listes
// de valeurs entières triées et dédupliquées.
//
// Le calcul de la prochaine occurrence (T-40) doit réutiliser ce type plutôt
// que reparser l'expression : c'est la raison d'être de sa structure en
// champs déjà résolus, plutôt qu'en simple chaîne conservée telle quelle.
type CronSchedule struct {
	// Raw est l'expression telle que fournie, conservée pour le journal et
	// le manifest (voir Config.LogValue).
	Raw string
	// Enabled vaut faux lorsque l'expression est vide : c'est un état valide
	// signifiant « ordonnanceur désactivé », pas une erreur de validation.
	// Voir le commentaire de Load sur MEDS_SYNC_CRON pour la justification
	// de ce choix, contre-intuitif au regard des autres variables.
	Enabled bool

	Minute     []int
	Hour       []int
	DayOfMonth []int
	Month      []int
	DayOfWeek  []int
}

// Bornes des cinq champs cron. Le jour de la semaine accepte 0 à 7 (7 est un
// alias de dimanche, comme dans la plupart des implémentations cron), 0
// restant la représentation canonique une fois normalisé.
const (
	minuteMin, minuteMax         = 0, 59
	hourMin, hourMax             = 0, 23
	dayOfMonthMin, dayOfMonthMax = 1, 31
	monthMin, monthMax           = 1, 12
	dayOfWeekMin, dayOfWeekMax   = 0, 7
)

// ParseCron valide syntaxiquement une expression cron à cinq champs et la
// résout en listes de valeurs entières. Seule la syntaxe est vérifiée ici
// (T-04) ; le calcul de la prochaine occurrence est laissé à T-40.
//
// Syntaxe acceptée par champ, conformément à la spécification de T-04 :
// `*` (toute la plage), listes séparées par des virgules (`1,15,30`), plages
// (`1-5`), et pas (`*/5`, `1-20/5`). Les intervalles hebdomadaires ne
// supportent pas de zéro-padding particulier : `01` et `1` sont équivalents,
// `strconv.Atoi` les traite de façon identique.
//
// Une expression vide (après retrait des espaces de tête et de fin) est
// acceptée et produit une planification désactivée (CronSchedule.Enabled ==
// false), conformément à docs/02-architecture.md §7 : « vide = scheduler
// désactivé (cas valide) ».
func ParseCron(expr string) (CronSchedule, error) {
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return CronSchedule{Raw: "", Enabled: false}, nil
	}

	fields := strings.Fields(raw)
	if len(fields) != 5 {
		return CronSchedule{}, fmt.Errorf(
			"expression %q invalide : 5 champs attendus (minute heure jour-du-mois mois jour-de-semaine), %d trouvé(s)",
			raw, len(fields),
		)
	}

	var errs []error

	minute, err := parseCronField(fields[0], minuteMin, minuteMax)
	if err != nil {
		errs = append(errs, fmt.Errorf("champ 'minute' %q : %w", fields[0], err))
	}
	hour, err := parseCronField(fields[1], hourMin, hourMax)
	if err != nil {
		errs = append(errs, fmt.Errorf("champ 'heure' %q : %w", fields[1], err))
	}
	dom, err := parseCronField(fields[2], dayOfMonthMin, dayOfMonthMax)
	if err != nil {
		errs = append(errs, fmt.Errorf("champ 'jour-du-mois' %q : %w", fields[2], err))
	}
	month, err := parseCronField(fields[3], monthMin, monthMax)
	if err != nil {
		errs = append(errs, fmt.Errorf("champ 'mois' %q : %w", fields[3], err))
	}
	dow, err := parseCronField(fields[4], dayOfWeekMin, dayOfWeekMax)
	if err != nil {
		errs = append(errs, fmt.Errorf("champ 'jour-de-semaine' %q : %w", fields[4], err))
	} else {
		dow = normalizeSevenToZero(dow)
	}

	if len(errs) > 0 {
		return CronSchedule{}, fmt.Errorf("expression cron %q invalide : %w", raw, errors.Join(errs...))
	}

	return CronSchedule{
		Raw:        raw,
		Enabled:    true,
		Minute:     minute,
		Hour:       hour,
		DayOfMonth: dom,
		Month:      month,
		DayOfWeek:  dow,
	}, nil
}

// normalizeSevenToZero convertit l'alias « 7 » (dimanche) en sa forme
// canonique « 0 », et déduplique le résultat s'il contenait déjà 0.
func normalizeSevenToZero(values []int) []int {
	seen := make(map[int]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if v == 7 {
			v = 0
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// parseCronField résout un champ cron (une liste séparée par des virgules
// de valeurs, plages ou pas) en une liste d'entiers triée et dédupliquée,
// bornée à [min, max].
func parseCronField(field string, min, max int) ([]int, error) {
	seen := make(map[int]bool)
	var out []int

	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, errors.New("élément vide (virgule superflue ?)")
		}

		base, step := part, 1
		if idx := strings.IndexByte(part, '/'); idx >= 0 {
			base = part[:idx]
			stepStr := part[idx+1:]
			s, err := strconv.Atoi(stepStr)
			if err != nil || s <= 0 {
				return nil, fmt.Errorf("pas %q invalide : entier strictement positif attendu", stepStr)
			}
			step = s
		}

		lo, hi, err := parseCronRange(base, min, max)
		if err != nil {
			return nil, err
		}

		for v := lo; v <= hi; v += step {
			if v < min || v > max {
				return nil, fmt.Errorf("valeur %d hors bornes [%d,%d]", v, min, max)
			}
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	sort.Ints(out)
	return out, nil
}

// parseCronRange résout la base d'un élément de champ cron : `*` (toute la
// plage), une valeur unique, ou une plage `a-b`.
func parseCronRange(base string, min, max int) (lo, hi int, err error) {
	switch {
	case base == "*":
		return min, max, nil
	case strings.Contains(base, "-"):
		bounds := strings.SplitN(base, "-", 2)
		l, errL := strconv.Atoi(bounds[0])
		h, errH := strconv.Atoi(bounds[1])
		if errL != nil || errH != nil {
			return 0, 0, fmt.Errorf("plage %q invalide : entiers attendus de part et d'autre de '-'", base)
		}
		if l > h {
			return 0, 0, fmt.Errorf("plage %q invalide : borne basse supérieure à la borne haute", base)
		}
		return l, h, nil
	default:
		v, err := strconv.Atoi(base)
		if err != nil {
			return 0, 0, fmt.Errorf("valeur %q invalide : entier attendu", base)
		}
		return v, v, nil
	}
}
