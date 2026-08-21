package bdpm

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxLineSize borne la taille d'une ligne acceptée par Scanner. Le défaut
// de bufio.Scanner (64 Kio) est insuffisant : les libellés SMR font 294
// octets en moyenne mais suivent une distribution à longue traîne
// (docs/01-analyse-source-bdpm.md §3.4). 1 Mio couvre confortablement la
// plus longue ligne mesurée tout en gardant une borne explicite, plutôt
// qu'un buffer non borné qui transformerait une ligne corrompue en
// épuisement mémoire.
const MaxLineSize = 1 << 20

// QuarantineFunc est le point d'entrée de la quarantaine (implémentation
// complète en T-09) : Scanner l'invoque pour toute ligne dont le nombre de
// colonnes ne correspond pas à celui attendu, plutôt que d'interrompre
// l'ingestion. reason est un message court en français, adapté à un
// journal d'exploitation ; raw est la ligne complète telle que lue (après
// retrait du seul CR final, voir le commentaire de Scan).
type QuarantineFunc func(file string, line int, reason string, raw string)

// Scanner lit un fichier plat de la BDPM ligne par ligne et le découpe en
// champs séparés par des tabulations.
//
// Il n'utilise délibérément pas encoding/csv. La documentation officielle
// de l'ANSM est formelle : « Il n'y a pas de délimiteurs de champs » — le
// guillemet y est un caractère ordinaire, sans la sémantique que lui prête
// encoding/csv (ouverture/fermeture de champ, échappement par doublement).
// Une comparaison exhaustive avec encoding/csv en mode LazyQuotes a montré
// des résultats identiques sur deux fichiers sur trois testés, et des
// divergences sur le troisième (CIS_CPD_bdpm.txt) : LazyQuotes y laisse
// traîner des retours chariot en fin de champ sur six lignes
// (docs/01-analyse-source-bdpm.md, piège P8). Le découpage brut
// (bufio.Scanner + strings.Split) est donc au moins aussi correct, sans
// dépendre d'un mode de tolérance dont le comportement exact est une boîte
// noire pour ce format.
type Scanner struct {
	file           string
	sc             *bufio.Scanner
	expectedFields int
	quarantine     QuarantineFunc

	line   int
	fields []string
	err    error
}

// NewScanner construit un Scanner pour le fichier nommé file (utilisé
// uniquement dans les messages d'erreur et de quarantaine), lisant r.
//
// expectedFields est le nombre de colonnes attendu pour ce fichier
// (docs/01-analyse-source-bdpm.md §3) ; une valeur <= 0 désactive le
// contrôle de colonnes (aucune ligne n'est alors mise en quarantaine pour
// ce motif). quarantine peut être nil : dans ce cas, les lignes
// disqualifiées sont silencieusement ignorées, comme si quarantine ne
// faisait rien — utile pour les tests qui ne s'intéressent qu'aux lignes
// valides.
func NewScanner(file string, r io.Reader, expectedFields int, quarantine QuarantineFunc) *Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLineSize)
	if quarantine == nil {
		quarantine = func(string, int, string, string) {}
	}
	return &Scanner{
		file:           file,
		sc:             sc,
		expectedFields: expectedFields,
		quarantine:     quarantine,
	}
}

// Scan avance vers le prochain enregistrement valide et retourne faux
// lorsqu'il n'y en a plus (fin de fichier) ou qu'une erreur irrécupérable
// est survenue (consulter Err() dans ce dernier cas).
//
// Trois catégories de lignes sont traitées sans jamais interrompre la
// lecture ni paniquer :
//
//   - Lignes vides (après retrait des espaces) : ignorées et non comptées
//     comme un enregistrement, y compris en fin de fichier
//     (docs/01-analyse-source-bdpm.md, piège P7 — CIS_CPD_bdpm.txt se
//     termine par des lignes vides).
//   - Lignes dont le nombre de colonnes diffère de expectedFields : mises
//     en quarantaine via QuarantineFunc et sautées. Une ligne perdue est un
//     incident mineur ; un refus d'ingestion pour une seule ligne mal
//     formée serait un incident majeur et disproportionné.
//   - Fin de fichier sans saut de ligne final (piège P7,
//     CIS_MITM.txt : 7 710 '\n' pour 7 711 enregistrements) : gérée
//     nativement par bufio.Scanner, qui restitue le dernier enregistrement
//     même non terminé par '\n'.
//
// Seul le CR final de chaque ligne est retiré (TrimRight), pas les CR
// isolés au milieu d'un champ (piège P7, six occurrences dans
// CIS_CPD_bdpm.txt) : bufio.ScanLines ne coupe que sur '\n', donc un CR
// interne à un enregistrement reste inclus dans le champ où il se trouve.
// C'est le comportement correct ici — la donnée est conservée littéralement
// plutôt que réinterprétée — mais il explique pourquoi TrimRight("\r") ne
// suffit pas à garantir l'absence de tout CR dans les champs retournés.
//
// Un guillemet nu au milieu d'un champ (piège P8) n'a aucun traitement
// spécial : strings.Split ne lui accorde aucune sémantique, il est donc
// conservé littéralement dans le champ, comme souhaité.
func (s *Scanner) Scan() bool {
	for s.sc.Scan() {
		s.line++
		raw := strings.TrimRight(s.sc.Text(), "\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}

		s.fields = splitTabFields(s.fields, raw)
		if s.expectedFields > 0 && len(s.fields) != s.expectedFields {
			s.quarantine(s.file, s.line, fmt.Sprintf(
				"nombre de colonnes inattendu : %d au lieu de %d", len(s.fields), s.expectedFields,
			), raw)
			continue
		}

		return true
	}

	if err := s.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			s.err = fmt.Errorf(
				"%s: ligne %d dépasse la taille maximale de %d octets : %w",
				s.file, s.line+1, MaxLineSize, err,
			)
		} else {
			s.err = fmt.Errorf("%s: lecture interrompue à la ligne %d : %w", s.file, s.line+1, err)
		}
	}
	return false
}

// Fields retourne les champs de l'enregistrement courant, découpés sur la
// tabulation.
//
// Le tampon retourné est réutilisé à chaque appel à Scan : il ne doit
// jamais être conservé au-delà de l'itération courante (pas d'ajout à une
// slice accumulée, pas de capture dans une closure différée). Un appelant
// qui a besoin de conserver des champs au-delà d'un appel à Scan doit les
// copier explicitement (par ex. via strings.Clone ou en les recopiant dans
// une structure propre). Ce choix évite une allocation de slice par ligne :
// le fichier le plus volumineux du corpus BDPM comporte 32 420 lignes
// (CIS_COMPO_bdpm.txt), et l'ingestion est benchmarkée en continu — voir
// BenchmarkScanner.
func (s *Scanner) Fields() []string {
	return s.fields
}

// Line retourne le numéro de la ligne physique courante, 1-indexé. C'est le
// numéro à utiliser pour tout message de quarantaine ou d'erreur destiné à
// un exploitant capable de rouvrir le fichier source à cette ligne.
func (s *Scanner) Line() int {
	return s.line
}

// Err retourne l'erreur irrécupérable ayant interrompu la lecture, ou nil
// si Scan s'est arrêté normalement en fin de fichier.
func (s *Scanner) Err() error {
	return s.err
}

// splitTabFields découpe line sur les tabulations en réutilisant la
// capacité de dst plutôt qu'en allouant une nouvelle slice à chaque appel
// (voir le commentaire de doc de Fields). Le découpage d'une chaîne Go ne
// copie jamais les octets sous-jacents ; seule l'allocation du tableau de
// pointeurs de chaînes (dst) est évitée ici, ce qui est le coût dominant
// pour un fichier à colonnes fixes appelé des dizaines de milliers de fois.
func splitTabFields(dst []string, line string) []string {
	dst = dst[:0]
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '\t' {
			dst = append(dst, line[start:i])
			start = i + 1
		}
	}
	return append(dst, line[start:])
}
