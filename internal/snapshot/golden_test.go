package snapshot

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

// update régénère les golden files.
//
//	go test ./internal/snapshot/ -run TestGolden -update
//
// Le mode existe pour que la mise à jour reste une opération explicite et
// tracée par le contrôle de version : régénérer sans le vouloir ferait
// disparaître la régression que le test devait signaler.
var update = flag.Bool("update", false, "régénère les golden files")

const (
	goldenDir    = "../../testdata/golden"
	goldenInput  = goldenDir + "/input"
	goldenExpect = goldenDir + "/expected"
)

// Les échantillons figés reproduisent les huit pièges de
// docs/01-analyse-source-bdpm.md §5, dans leur **encodage d'origine** : huit
// fichiers en Windows-1252, neuf en CRLF. C'est ce qui donne au test sa
// valeur — un échantillon ré-encodé en UTF-8 ne prouverait rien du
// comportement réel.
//
// Chaque piège porte une marque explicite dans les données, pour qu'une
// régression désigne elle-même ce qu'elle a cassé.
type goldenFile struct {
	name string
	// windows1252 indique que le contenu doit être encodé en Windows-1252
	// avant écriture (P1).
	windows1252 bool
	// crlf indique des fins de ligne CRLF (P7).
	crlf bool
	// noFinalNewline reproduit l'absence de saut de ligne final (P7).
	noFinalNewline bool
	// trailingBlank ajoute des lignes vides finales (P7).
	trailingBlank bool
	rows          [][]string
}

func goldenFiles() []goldenFile {
	return []goldenFile{
		{
			// P1 : CIS_bdpm.txt est en Windows-1252. P7 : CRLF.
			// P4 : dates JJ/MM/AAAA, multi-valeurs sur « ; ».
			name: bdpm.FileSpecialites, windows1252: true, crlf: true,
			rows: [][]string{
				{"60002283", "DOLIPRANE 1000 mg, comprimé", "comprimé", "orale",
					"Autorisation active", "Procédure nationale", "Commercialisée",
					"19/03/1997", "", "", "OPELLA HEALTHCARE FRANCE", "Non"},
				{"60002284", "AMLOR 5 mg, gélule", "gélule", "orale;intraveineuse",
					"Autorisation active", "Procédure nationale", "Non commercialisée",
					"01/06/1990", "Alerte", "EU/1/00/000/001", "VIATRIS SANTÉ;PFIZER", "Oui"},
				{"60002285", "ACIDE ACÉTYLSALICYLIQUE & VITAMINE C, comprimé effervescent",
					"comprimé effervescent", "orale", "Autorisation active",
					"Procédure nationale", "Commercialisée", "12/06/1995", "", "",
					"BAYER HEALTHCARE", "Non"},
			},
		},
		{
			// P1 : ce fichier-là est en UTF-8, contrairement au précédent.
			// P4 : prix décimaux à virgule, taux avec et sans espace avant %.
			// Le troisième prix porte un séparateur de milliers (« 1 234,56 »),
			// piège découvert le 15/08/2026 et documenté sous P4.
			name: bdpm.FilePresentations, crlf: true,
			rows: [][]string{
				{"60002283", "3000001", "plaquette(s) thermoformée(s) de 8 comprimé(s)",
					"Présentation active", "Déclaration de commercialisation", "16/03/2011",
					"3400930000011", "oui", "65 %", "1,78", "2,80", "1,02", ""},
				{"60002283", "3000002", "plaquette de 16 comprimé(s)",
					"Présentation active", "Déclaration de commercialisation", "16/03/2011",
					"3400930000028", "non", "65%", "", "", "", ""},
				{"60002284", "3000003", "flacon(s) de 30 gélule(s)",
					"Présentation active", "Déclaration de commercialisation", "01/01/2001",
					"3400930000035", "inconnu", "30 %", "1 234,56", "1 500,00", "1,02",
					"Prise en charge sous conditions"},
				// P6 : CIS orphelin — absent de CIS_bdpm.txt.
				{"99999999", "3000004", "boîte orpheline", "Présentation active",
					"Déclaration de commercialisation", "01/01/2020", "3400930000042",
					"oui", "65 %", "1,00", "1,50", "1,02", ""},
			},
		},
		{
			// P3 : la nature « ST » doit être acceptée et convertie en « FT ».
			// Dosages homéopathiques laissés bruts.
			name: bdpm.FileComposants, windows1252: true, crlf: true,
			rows: [][]string{
				{"60002283", "comprimé", "42215", "PARACÉTAMOL", "1000,00 mg", "un comprimé", "SA", "1"},
				{"60002284", "gélule", "12345", "BÉSILATE D'AMLODIPINE", "6,94 mg", "une gélule", "SA", "1"},
				{"60002284", "gélule", "12346", "AMLODIPINE", "5,00 mg", "une gélule", "ST", "1"},
				{"60002285", "comprimé", "54321", "ACIDE ACÉTYLSALICYLIQUE",
					"2CH à 30CH et 4DH à 60DH", "un comprimé", "SA", "1"},
			},
		},
		{
			// Dates au format compact AAAAMMJJ, distinct du reste de la source.
			name: bdpm.FileAvisSMR, windows1252: true, crlf: true,
			rows: [][]string{
				{"60002283", "CT-1234", "Renouvellement d'inscription", "20150610",
					"Important", "Le service médical rendu reste important."},
				{"60002284", "CT-5678", "Inscription", "20200115", "Modéré",
					"Le service médical rendu est modéré."},
			},
		},
		{
			// Niveau ASMR en chiffres romains, sauf la valeur textuelle qui doit
			// laisser le niveau à nil.
			name: bdpm.FileAvisASMR, windows1252: true, crlf: true,
			rows: [][]string{
				{"60002283", "CT-1234", "Renouvellement d'inscription", "20150610", "IV",
					"Amélioration mineure."},
				{"60002284", "CT-5678", "Inscription", "20200115",
					"Commentaires sans chiffrage de l'ASMR", "Sans objet."},
			},
		},
		{
			// P4 : les URL de la HAS sont en HTTP et doivent être réécrites.
			name: bdpm.FileLiensCT, crlf: true,
			rows: [][]string{
				{"CT-1234", "http://www.has-sante.fr/portail/jcms/ct-1234"},
				{"CT-5678", "http://www.has-sante.fr/portail/jcms/ct-5678"},
			},
		},
		{
			// Énumération discontinue : 0, 1, 2, 4 — la valeur 3 n'existe pas.
			name: bdpm.FileGroupes, windows1252: true, crlf: true,
			rows: [][]string{
				{"1", "PARACÉTAMOL 1000 mg - DOLIPRANE 1000 mg, comprimé", "60002283", "0", "1"},
				{"1", "PARACÉTAMOL 1000 mg - DOLIPRANE 1000 mg, comprimé", "60002285", "4", "2"},
				// P6 : membre orphelin — le groupe doit survivre sans lui.
				{"2", "GROUPE ENTIÈREMENT HISTORIQUE", "99999998", "0", "1"},
			},
		},
		{
			// P7 : ce fichier se termine par des lignes vides.
			// P8 : un guillemet nu, qu'encoding/csv interpréterait à tort.
			name: bdpm.FileConditions, windows1252: true, crlf: true, trailingBlank: true,
			rows: [][]string{
				{"60002283", "liste I"},
				{"60002284", `prescription réservée aux spécialistes en "cardiologie"`},
				{"60002285", "médicament non soumis à prescription médicale"},
			},
		},
		{
			// P5 : les libellés de statut sont incohérents avec les codes ;
			// c'est le **code** qui fait foi, le libellé source étant conservé
			// pour l'audit.
			name: bdpm.FileRuptures, windows1252: true, crlf: true,
			rows: [][]string{
				{"60002283", "3400930000011", "2", "Remise à disposition", "05/01/2026",
					"01/02/2026", "", "http://ansm.sante.fr/S-informer/fiche-1"},
				{"60002284", "", "1", "Tension", "01/12/2025", "15/01/2026", "20/01/2026",
					"http://ansm.sante.fr/S-informer/fiche-2"},
			},
		},
		{
			// P7 : pas de saut de ligne final, et fins de ligne LF ici.
			name: bdpm.FileMITM, noFinalNewline: true,
			rows: [][]string{
				{"60002283", "N02BE01", "PARACETAMOL", "http://base-donnees-publique.medicaments.gouv.fr/x"},
				{"60002284", "C08CA01", "AMLODIPINE", "http://base-donnees-publique.medicaments.gouv.fr/y"},
			},
		},
	}
}

// render produit le contenu binaire exact d'un échantillon.
func (g goldenFile) render(t *testing.T) []byte {
	t.Helper()

	eol := "\n"
	if g.crlf {
		eol = "\r\n"
	}
	var b strings.Builder
	for _, row := range g.rows {
		b.WriteString(strings.Join(row, "\t"))
		b.WriteString(eol)
	}
	content := b.String()
	if g.noFinalNewline {
		content = strings.TrimSuffix(content, eol)
	}
	if g.trailingBlank {
		content += eol + eol
	}

	if !g.windows1252 {
		return []byte(content)
	}
	encoded, err := encodeWindows1252Bytes(content)
	if err != nil {
		t.Fatalf("encodage Windows-1252 de %s : %v", g.name, err)
	}
	return encoded
}

// TestGolden_EcrireEtRelire est le test de non-régression du pipeline
// d'ingestion : les dix échantillons figés produisent un snapshot dont chaque
// octet est comparé au snapshot attendu.
//
// Une modification non intentionnelle d'un parseur — un format de date, un
// arrondi de prix, un libellé dérivé — fait échouer ce test en désignant le
// fichier fautif.
func TestGolden_EcrireEtRelire(t *testing.T) {
	files := goldenFiles()

	if *update {
		if err := os.MkdirAll(goldenInput, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, g := range files {
			path := filepath.Join(goldenInput, g.name)
			if err := os.WriteFile(path, g.render(t), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Les entrées sont relues **depuis le disque**, jamais régénérées en
	// mémoire : c'est ce qui vérifie qu'un aller-retour Git n'a pas altéré
	// l'encodage ni les fins de ligne.
	res := bdpm.DatasetResult{Hash: "golden"}
	for _, g := range files {
		raw, err := os.ReadFile(filepath.Join(goldenInput, g.name))
		if err != nil {
			t.Fatalf("échantillon manquant (%s) — régénérer avec -update : %v", g.name, err)
		}
		res.Files = append(res.Files, bdpm.FileResult{Name: g.name, Bytes: raw, SHA256: "x"})
	}

	q := bdpm.NewQuarantine()
	ds, err := bdpm.ParseAll(res, q)
	if err != nil {
		t.Fatalf("ParseAll : %v", err)
	}
	bdpm.CheckReferentialIntegrity(ds, q)
	ds.Derive()

	dir := t.TempDir()
	fixed := time.Date(2026, 8, 15, 4, 12, 33, 0, time.UTC)
	out, err := Write(ds, q, WriteOptions{
		DataDir: dir, Generator: "golden/1.0.0", Hash: "golden",
		Now: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("Write : %v", err)
	}

	entries, err := os.ReadDir(out.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.RemoveAll(goldenExpect); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(goldenExpect, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, e := range entries {
		got, err := os.ReadFile(filepath.Join(out.Dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		expectPath := filepath.Join(goldenExpect, e.Name())
		if *update {
			if err := os.WriteFile(expectPath, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(expectPath)
		if err != nil {
			t.Errorf("snapshot attendu manquant (%s) — régénérer avec -update", e.Name())
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s diffère du snapshot attendu.\n%s", e.Name(), firstDiff(want, got))
		}
	}

	if *update {
		t.Logf("golden files régénérés : %d entrées, %d fichiers de snapshot",
			len(files), len(entries))
	}
}

// firstDiff localise la première divergence, en montrant les lignes autour :
// un message « les octets diffèrent » sans contexte oblige à ouvrir les deux
// fichiers à la main.
func firstDiff(want, got []byte) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return "  ligne " + strconv.Itoa(i+1) + "\n  attendu : " + truncateLine(w) + "\n  obtenu  : " + truncateLine(g)
		}
	}
	return "  (contenu identique ligne à ligne mais octets différents : fin de ligne ?)"
}

func truncateLine(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
