package bdpm

import (
	"strconv"
	"strings"
	"testing"
)

// collect exécute le Scanner jusqu'à épuisement et retourne une copie de
// chaque enregistrement valide (chaque appel copie explicitement les
// champs, car Fields() réutilise son tampon).
func collect(t *testing.T, sc *Scanner) [][]string {
	t.Helper()
	var out [][]string
	for sc.Scan() {
		fields := sc.Fields()
		cp := make([]string, len(fields))
		copy(cp, fields)
		out = append(out, cp)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("Scanner.Err() inattendu : %v", err)
	}
	return out
}

// TestScanner_P7a_NoTrailingNewline reproduit CIS_MITM.txt : 7 710 '\n'
// pour 7 711 enregistrements, sans saut de ligne final. Un lecteur naïf
// perdrait le dernier enregistrement.
func TestScanner_P7a_NoTrailingNewline(t *testing.T) {
	t.Parallel()

	const n = 50
	var sb strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("CIS" + strconv.Itoa(i) + "\tATC" + strconv.Itoa(i) + "\tNom\thttp://lien")
	}
	// Pas de '\n' final, volontairement.
	content := sb.String()
	if strings.HasSuffix(content, "\n") {
		t.Fatal("le corpus de test ne doit pas se terminer par un saut de ligne")
	}

	sc := NewScanner("CIS_MITM.txt", strings.NewReader(content), 4, nil)
	records := collect(t, sc)

	if len(records) != n {
		t.Fatalf("len(records) = %d, attendu %d (dernier enregistrement perdu ?)", len(records), n)
	}
	last := records[n-1]
	if last[0] != "CIS"+strconv.Itoa(n-1) {
		t.Errorf("dernier enregistrement = %v, code CIS attendu %q", last, "CIS"+strconv.Itoa(n-1))
	}
}

// TestScanner_P7b_InternalCarriageReturn prouve que les CR isolés au milieu
// d'un champ (CIS_CPD_bdpm.txt : 6 occurrences) sont conservés tels quels,
// et non interprétés comme une fin d'enregistrement. bufio.ScanLines ne
// coupe que sur '\n' ; seul le CR final de la ligne physique est retiré par
// TrimRight, jamais un CR interne.
func TestScanner_P7b_InternalCarriageReturn(t *testing.T) {
	t.Parallel()

	// Une ligne unique (terminée par LF) dont le second champ contient un
	// CR isolé au milieu du texte.
	content := "60000001\tliste I\rincomplète\n60000002\tliste II\n"

	sc := NewScanner("CIS_CPD_bdpm.txt", strings.NewReader(content), 2, nil)
	records := collect(t, sc)

	if len(records) != 2 {
		t.Fatalf("len(records) = %d, attendu 2", len(records))
	}
	want := "liste I\rincomplète"
	if records[0][1] != want {
		t.Errorf("champ = %q, attendu %q (CR interne conservé littéralement)", records[0][1], want)
	}
}

// TestScanner_P7c_TrailingBlankLines vérifie que les lignes vides en fin de
// fichier (CIS_CPD_bdpm.txt) sont sautées sans être comptées comme un
// enregistrement.
func TestScanner_P7c_TrailingBlankLines(t *testing.T) {
	t.Parallel()

	content := "60000001\tliste I\n60000002\tliste II\n\n\n   \n"

	sc := NewScanner("CIS_CPD_bdpm.txt", strings.NewReader(content), 2, nil)
	records := collect(t, sc)

	if len(records) != 2 {
		t.Fatalf("len(records) = %d, attendu 2 (les lignes vides finales ne doivent pas compter)", len(records))
	}
	if sc.Line() != 5 {
		t.Errorf("Line() = %d après lecture complète, attendu 5 (numérotation physique)", sc.Line())
	}
}

// TestScanner_P8_BareQuotes prouve qu'un guillemet nu au milieu d'un champ
// non délimité est conservé littéralement, sans déclencher la sémantique
// d'encoding/csv (ouverture de champ, erreur "bare quote").
func TestScanner_P8_BareQuotes(t *testing.T) {
	t.Parallel()

	content := "60000001\tforme \"spéciale\" comprimé\tvoie\n"

	sc := NewScanner("CIS_bdpm.txt", strings.NewReader(content), 3, nil)
	records := collect(t, sc)

	if len(records) != 1 {
		t.Fatalf("len(records) = %d, attendu 1", len(records))
	}
	want := `forme "spéciale" comprimé`
	if records[0][1] != want {
		t.Errorf("champ = %q, attendu %q (guillemets conservés littéralement)", records[0][1], want)
	}
}

// TestScanner_MixedLineEndings vérifie qu'un corpus mêlant CRLF et LF (le
// cas réel : CIS_bdpm.txt en CRLF, CIS_CIP_bdpm.txt en LF) est traité
// uniformément.
func TestScanner_MixedLineEndings(t *testing.T) {
	t.Parallel()

	content := "60000001\tA\r\n60000002\tB\n60000003\tC\r\n"

	sc := NewScanner("mixte.txt", strings.NewReader(content), 2, nil)
	records := collect(t, sc)

	if len(records) != 3 {
		t.Fatalf("len(records) = %d, attendu 3", len(records))
	}
	for i, want := range []string{"A", "B", "C"} {
		if records[i][1] != want {
			t.Errorf("enregistrement %d, champ 1 = %q, attendu %q", i, records[i][1], want)
		}
		if strings.ContainsAny(records[i][1], "\r\n") {
			t.Errorf("enregistrement %d contient un terminateur de ligne résiduel : %q", i, records[i][1])
		}
	}
}

// TestScanner_LineTooLong vérifie qu'une ligne dépassant MaxLineSize
// produit une erreur explicite portant le numéro de ligne, sans panique ni
// troncature silencieuse.
func TestScanner_LineTooLong(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", MaxLineSize+1)
	content := "60000001\tOK\n60000002\t" + oversized + "\n60000003\tOK\n"

	sc := NewScanner("CIS_HAS_SMR_bdpm.txt", strings.NewReader(content), 2, nil)

	// La première ligne, valide, doit être restituée avant l'échec.
	if !sc.Scan() {
		t.Fatalf("Scan() aurait dû réussir sur la première ligne ; erreur : %v", sc.Err())
	}
	if got := sc.Fields()[0]; got != "60000001" {
		t.Fatalf("premier enregistrement inattendu : %v", sc.Fields())
	}

	if sc.Scan() {
		t.Fatalf("Scan() aurait dû échouer sur la ligne trop longue, obtenu %v", sc.Fields())
	}
	err := sc.Err()
	if err == nil {
		t.Fatal("Err() ne doit pas être nil après une ligne trop longue")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("le message d'erreur devrait mentionner le numéro de ligne (2) : %v", err)
	}
}

// TestScanner_UnexpectedColumnCount_Quarantine vérifie qu'une ligne dont le
// nombre de colonnes diffère de l'attendu est mise en quarantaine — jamais
// fatale — et que la lecture continue normalement ensuite.
func TestScanner_UnexpectedColumnCount_Quarantine(t *testing.T) {
	t.Parallel()

	content := "60000001\tA\tB\n" + // 3 colonnes, attendu 2 -> quarantaine
		"60000002\tC\n" + // valide
		"60000003\n" + // 1 colonne -> quarantaine
		"60000004\tD\n" // valide

	type entry struct {
		file, reason, raw string
		line              int
	}
	var quarantined []entry
	quarantine := func(file string, line int, reason string, raw string) {
		quarantined = append(quarantined, entry{file, reason, raw, line})
	}

	sc := NewScanner("CIS_bdpm.txt", strings.NewReader(content), 2, quarantine)
	records := collect(t, sc)

	if len(records) != 2 {
		t.Fatalf("len(records) = %d, attendu 2 (lignes valides uniquement)", len(records))
	}
	if records[0][0] != "60000002" || records[1][0] != "60000004" {
		t.Errorf("enregistrements valides inattendus : %v", records)
	}

	if len(quarantined) != 2 {
		t.Fatalf("len(quarantined) = %d, attendu 2", len(quarantined))
	}
	if quarantined[0].line != 1 || quarantined[1].line != 3 {
		t.Errorf("numéros de ligne en quarantaine inattendus : %+v", quarantined)
	}
	for _, q := range quarantined {
		if q.file != "CIS_bdpm.txt" {
			t.Errorf("nom de fichier en quarantaine inattendu : %q", q.file)
		}
		if q.reason == "" {
			t.Error("motif de quarantaine vide")
		}
	}
}

// TestScanner_NoColumnCheck vérifie que expectedFields <= 0 désactive le
// contrôle de colonnes.
func TestScanner_NoColumnCheck(t *testing.T) {
	t.Parallel()

	content := "a\tb\tc\nd\n"
	sc := NewScanner("libre.txt", strings.NewReader(content), 0, nil)
	records := collect(t, sc)
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, attendu 2", len(records))
	}
}

// TestScanner_EmptyInput vérifie qu'un contenu vide n'est pas fatal.
func TestScanner_EmptyInput(t *testing.T) {
	t.Parallel()

	sc := NewScanner("vide.txt", strings.NewReader(""), 2, nil)
	if sc.Scan() {
		t.Fatal("Scan() sur un contenu vide devrait retourner faux immédiatement")
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("Err() inattendu sur un contenu vide : %v", err)
	}
}

// TestScanner_NilQuarantine vérifie qu'un callback de quarantaine nil ne
// panique pas.
func TestScanner_NilQuarantine(t *testing.T) {
	t.Parallel()

	content := "a\tb\tc\nd\te\n"
	sc := NewScanner("x.txt", strings.NewReader(content), 2, nil)
	records := collect(t, sc)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, attendu 1", len(records))
	}
}

// TestScanner_FieldsBufferReused documente et vérifie explicitement que le
// tampon retourné par Fields() est réutilisé entre deux appels à Scan.
func TestScanner_FieldsBufferReused(t *testing.T) {
	t.Parallel()

	content := "a\tb\nc\td\n"
	sc := NewScanner("x.txt", strings.NewReader(content), 2, nil)

	if !sc.Scan() {
		t.Fatal("premier Scan() attendu")
	}
	first := sc.Fields()

	if !sc.Scan() {
		t.Fatal("second Scan() attendu")
	}
	second := sc.Fields()

	if &first[0] != &second[0] {
		// Non fatal : documente juste le comportement attendu (réutilisation
		// du tampon), qui est un choix de performance et non une garantie
		// absolue d'identité de pointeur sur toutes les implémentations
		// futures. On vérifie en revanche que le contenu de `first` a bien
		// été écrasé, ce qui est la conséquence observable importante.
		t.Log("le tampon sous-jacent n'est pas partagé par adresse ; comportement non garanti par l'API")
	}
	if first[0] != "c" {
		t.Errorf("le tampon de first aurait dû être écrasé par le second Scan() : first[0] = %q, attendu \"c\"", first[0])
	}
}

func BenchmarkScanner(b *testing.B) {
	// Corpus synthétique représentatif du plus gros fichier réel
	// (CIS_COMPO_bdpm.txt, 32 420 lignes, 8 colonnes).
	const lines = 32420
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString("60002283\tcomprimé\t42215\tANASTROZOLE\t1,00 mg\tun comprimé\tSA\t12345\n")
	}
	content := sb.String()

	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sc := NewScanner("CIS_COMPO_bdpm.txt", strings.NewReader(content), 8, nil)
		count := 0
		for sc.Scan() {
			count++
		}
		if err := sc.Err(); err != nil {
			b.Fatalf("erreur inattendue : %v", err)
		}
		if count != lines {
			b.Fatalf("count = %d, attendu %d", count, lines)
		}
	}
}
