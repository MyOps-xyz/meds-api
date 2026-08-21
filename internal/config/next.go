package config

import "time"

// Next calcule la prochaine occurrence strictement postérieure à after.
//
// L'implémentation avance minute par minute plutôt que de résoudre les
// champs analytiquement. C'est volontairement naïf : le pire cas — une
// expression qui ne se déclenche qu'un 29 février — coûte quelques
// centaines de milliers d'itérations triviales, une fois par
// déclenchement, sur un service qui synchronise une fois par jour. La
// complexité d'un calcul exact ne se justifierait que pour un ordonnanceur
// gérant des milliers de tâches.
//
// La borne de recherche est de quatre ans : elle couvre le cycle bissextile
// complet, de sorte qu'une expression satisfiable est toujours trouvée, et
// qu'une expression qui ne l'est pas (31 février) est détectée plutôt que de
// boucler indéfiniment.
func (c CronSchedule) Next(after time.Time) (time.Time, bool) {
	if !c.Enabled {
		return time.Time{}, false
	}

	// On repart de la minute suivante, secondes remises à zéro : une
	// occurrence est un instant à la minute, et « strictement postérieure »
	// évite de redéclencher la minute courante.
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(4, 0, 0)

	for !t.After(limit) {
		if c.matches(t) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

func (c CronSchedule) matches(t time.Time) bool {
	if !contains(c.Minute, t.Minute()) ||
		!contains(c.Hour, t.Hour()) ||
		!contains(c.Month, int(t.Month())) {
		return false
	}

	// Sémantique cron historique : lorsque le jour du mois **et** le jour de
	// la semaine sont tous deux restreints, l'occurrence a lieu si l'un ou
	// l'autre correspond — et non les deux. Le cas « tous les 1ᵉʳ du mois et
	// tous les lundis » serait autrement inexprimable.
	domRestricted := len(c.DayOfMonth) != dayOfMonthMax-dayOfMonthMin+1
	dowRestricted := len(c.DayOfWeek) != 7

	domMatch := contains(c.DayOfMonth, t.Day())
	dowMatch := contains(c.DayOfWeek, int(t.Weekday()))

	switch {
	case domRestricted && dowRestricted:
		return domMatch || dowMatch
	case domRestricted:
		return domMatch
	case dowRestricted:
		return dowMatch
	default:
		return true
	}
}

func contains(values []int, v int) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
