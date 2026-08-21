package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCron(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    string
		want    CronSchedule
		wantErr bool
	}{
		{
			name: "expression vide désactive l'ordonnanceur",
			expr: "",
			want: CronSchedule{Raw: "", Enabled: false},
		},
		{
			name: "expression composée uniquement d'espaces désactive l'ordonnanceur",
			expr: "   ",
			want: CronSchedule{Raw: "", Enabled: false},
		},
		{
			name: "quotidien à 04:00",
			expr: "0 4 * * *",
			want: CronSchedule{
				Raw:        "0 4 * * *",
				Enabled:    true,
				Minute:     []int{0},
				Hour:       []int{4},
				DayOfMonth: fullRange(dayOfMonthMin, dayOfMonthMax),
				Month:      fullRange(monthMin, monthMax),
				DayOfWeek:  fullRange(dayOfWeekMin, 6),
			},
		},
		{
			name: "listes",
			expr: "0,30 8,20 1,15 1,6,12 1,3,5",
			want: CronSchedule{
				Raw:        "0,30 8,20 1,15 1,6,12 1,3,5",
				Enabled:    true,
				Minute:     []int{0, 30},
				Hour:       []int{8, 20},
				DayOfMonth: []int{1, 15},
				Month:      []int{1, 6, 12},
				DayOfWeek:  []int{1, 3, 5},
			},
		},
		{
			name: "plages",
			expr: "0-5 9-17 1-5 1-3 1-5",
			want: CronSchedule{
				Raw:        "0-5 9-17 1-5 1-3 1-5",
				Enabled:    true,
				Minute:     []int{0, 1, 2, 3, 4, 5},
				Hour:       []int{9, 10, 11, 12, 13, 14, 15, 16, 17},
				DayOfMonth: []int{1, 2, 3, 4, 5},
				Month:      []int{1, 2, 3},
				DayOfWeek:  []int{1, 2, 3, 4, 5},
			},
		},
		{
			name: "pas",
			expr: "*/15 */6 1 * *",
			want: CronSchedule{
				Raw:        "*/15 */6 1 * *",
				Enabled:    true,
				Minute:     []int{0, 15, 30, 45},
				Hour:       []int{0, 6, 12, 18},
				DayOfMonth: []int{1},
				Month:      fullRange(monthMin, monthMax),
				DayOfWeek:  fullRange(dayOfWeekMin, 6),
			},
		},
		{
			name: "plage avec pas",
			expr: "0-30/10 * * * *",
			want: CronSchedule{
				Raw:        "0-30/10 * * * *",
				Enabled:    true,
				Minute:     []int{0, 10, 20, 30},
				Hour:       fullRange(hourMin, hourMax),
				DayOfMonth: fullRange(dayOfMonthMin, dayOfMonthMax),
				Month:      fullRange(monthMin, monthMax),
				DayOfWeek:  fullRange(dayOfWeekMin, 6),
			},
		},
		{
			name: "alias 7 pour dimanche normalisé en 0",
			expr: "0 0 * * 7",
			want: CronSchedule{
				Raw:        "0 0 * * 7",
				Enabled:    true,
				Minute:     []int{0},
				Hour:       []int{0},
				DayOfMonth: fullRange(dayOfMonthMin, dayOfMonthMax),
				Month:      fullRange(monthMin, monthMax),
				DayOfWeek:  []int{0},
			},
		},
		{
			name: "0 et 7 dédupliqués sur jour-de-semaine",
			expr: "0 0 * * 0,7",
			want: CronSchedule{
				Raw:        "0 0 * * 0,7",
				Enabled:    true,
				Minute:     []int{0},
				Hour:       []int{0},
				DayOfMonth: fullRange(dayOfMonthMin, dayOfMonthMax),
				Month:      fullRange(monthMin, monthMax),
				DayOfWeek:  []int{0},
			},
		},
		{name: "trop peu de champs", expr: "0 4 * *", wantErr: true},
		{name: "trop de champs", expr: "0 4 * * * *", wantErr: true},
		{name: "minute hors bornes", expr: "60 4 * * *", wantErr: true},
		{name: "heure hors bornes", expr: "0 24 * * *", wantErr: true},
		{name: "jour du mois hors bornes (0)", expr: "0 4 0 * *", wantErr: true},
		{name: "jour du mois hors bornes (32)", expr: "0 4 32 * *", wantErr: true},
		{name: "mois hors bornes (0)", expr: "0 4 1 0 *", wantErr: true},
		{name: "mois hors bornes (13)", expr: "0 4 1 13 *", wantErr: true},
		{name: "jour de semaine hors bornes (8)", expr: "0 4 * * 8", wantErr: true},
		{name: "valeur non numérique", expr: "a 4 * * *", wantErr: true},
		{name: "plage inversée", expr: "30-0 4 * * *", wantErr: true},
		{name: "pas nul", expr: "*/0 4 * * *", wantErr: true},
		{name: "pas négatif", expr: "*/-5 4 * * *", wantErr: true},
		{name: "pas non numérique", expr: "*/a 4 * * *", wantErr: true},
		{name: "élément vide dans une liste", expr: "0,,30 4 * * *", wantErr: true},
		{name: "plusieurs champs invalides à la fois", expr: "60 24 32 13 8", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCron(tt.expr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseCron(%q) = %+v, nil ; erreur attendue", tt.expr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCron(%q) erreur inattendue : %v", tt.expr, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseCron(%q) = %+v, attendu %+v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestParseCron_MultiFieldErrorAggregation vérifie que les erreurs des cinq
// champs sont toutes rapportées lorsque plusieurs sont invalides, et pas
// seulement la première rencontrée.
func TestParseCron_MultiFieldErrorAggregation(t *testing.T) {
	t.Parallel()

	_, err := ParseCron("60 24 32 13 8")
	if err == nil {
		t.Fatal("erreur attendue")
	}
	msg := err.Error()
	for _, want := range []string{"minute", "heure", "jour-du-mois", "mois", "jour-de-semaine"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message d'erreur %q ne mentionne pas le champ %q", msg, want)
		}
	}
}

func fullRange(min, max int) []int {
	out := make([]int, 0, max-min+1)
	for v := min; v <= max; v++ {
		out = append(out, v)
	}
	return out
}
