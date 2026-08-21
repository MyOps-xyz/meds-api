//go:build live

// Ces tests interrogent la vraie source de l'ANSM. Ils sont exclus par
// défaut : la CI ne doit pas dépendre d'un service tiers, et l'ANSM n'a pas
// à absorber le trafic de chaque exécution de `go test`.
//
//	go test -tags live -v ./internal/bdpm/
//
// Ils existent parce que plusieurs critères d'acceptation des tâches T-05,
// T-06 et T-07 portent explicitement sur les *fichiers réels* : un
// httptest.Server prouve que le client se comporte correctement face à un
// serveur, jamais que la donnée de l'ANSM est bien celle qu'on croit. Ce
// sont aussi ces tests qui détecteront un changement de format amont — le
// risque principal du projet.
package bdpm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestLive_FetchAll vérifie les critères de T-05 (les dix fichiers récupérés
// en moins de 60 s), de T-06 (encodage détecté par fichier) et la volumétrie
// mesurée dans docs/01-analyse-source-bdpm.md §2.
func TestLive_FetchAll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	files := DefaultFiles()
	if len(files) != 10 {
		t.Fatalf("DefaultFiles() = %d fichiers, attendu 10", len(files))
	}

	start := time.Now()
	res, err := NewClient("test").FetchAll(ctx, files)
	if err != nil {
		t.Fatalf("FetchAll() : %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 60*time.Second {
		t.Errorf("FetchAll() a duré %v, budget T-05 : 60 s", elapsed)
	}

	// Encodages mesurés le 14/08/2026 (docs/01 §2). Un écart n'est pas
	// forcément un défaut : l'ANSM migre ses fichiers vers UTF-8 un par un.
	// Le test le signale sans échouer, car c'est précisément ce que la
	// détection par fichier est censée absorber (piège P1).
	expectedEncoding := map[string]string{
		"CIS_bdpm.txt":     EncodingWindows1252,
		"CIS_CIP_bdpm.txt": EncodingUTF8,
	}

	var total int
	for _, f := range res.Files {
		total += len(f.Bytes)

		text, enc, err := DecodeFile(f.Bytes)
		if err != nil {
			t.Errorf("%s : DecodeFile() a échoué : %v", f.Name, err)
			continue
		}
		if want, ok := expectedEncoding[f.Name]; ok && enc != want {
			t.Logf("ATTENTION %s : encodage %q, %q lors de la mesure du 14/08/2026 — "+
				"migration amont probable, à répercuter dans docs/01", f.Name, enc, want)
		}
		if len(f.Bytes) == 0 {
			t.Errorf("%s : fichier vide", f.Name)
		}
		if len(f.SHA256) != 64 {
			t.Errorf("%s : SHA256 = %q, attendu 64 caractères hexadécimaux", f.Name, f.SHA256)
		}
		if strings.ContainsRune(text, '�') {
			t.Errorf("%s : le texte décodé contient le caractère de remplacement U+FFFD, "+
				"signe d'une détection d'encodage fautive", f.Name)
		}
		t.Logf("%-24s %8d octets  %-13s %d tentative(s)  %v", f.Name, len(f.Bytes), enc, f.Attempts, f.Duration)
	}

	// 22,3 Mo mesurés. Une variation de plus de 20 % signale un changement
	// amont qui mérite une relecture de docs/01 (même garde-fou de
	// vraisemblance qu'à l'ingestion, docs/09 §3.6).
	const measuredTotal = 22_300_000
	if ratio := float64(total) / measuredTotal; ratio < 0.8 || ratio > 1.2 {
		t.Errorf("volume total = %d octets, %d±20%% attendus d'après la mesure du 14/08/2026", total, measuredTotal)
	}
	if len(res.Hash) != 64 {
		t.Errorf("hash global = %q, attendu 64 caractères hexadécimaux", res.Hash)
	}
	t.Logf("total %d octets en %v, hash %s", total, elapsed, res.Hash)
}

// TestLive_ScannerRecordCounts vérifie le critère de T-07 le plus facile à
// rater : CIS_MITM.txt n'a pas de saut de ligne final, et un lecteur naïf y
// perd son dernier enregistrement.
//
// La colonne `lines` reprend les comptes de docs/01-analyse-source-bdpm.md §2,
// obtenus par `awk 'END{print NR}'`, c'est-à-dire un décompte de sauts de
// ligne. La colonne `blank` en retranche les lignes qui ne portent aucune
// donnée et qu'un lecteur d'enregistrements doit sauter (piège P7c) : elle
// vaut zéro partout sauf sur CIS_CPD_bdpm.txt, dont trois lignes ne
// contiennent qu'un retour chariot isolé. La distinction compte, parce que
// « 28 379 » n'est pas le nombre d'enregistrements de ce fichier.
func TestLive_ScannerRecordCounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cases := []struct {
		file   string
		fields int
		lines  int
		blank  int
	}{
		{"CIS_bdpm.txt", 12, 15857, 0},
		{"CIS_CIP_bdpm.txt", 13, 20903, 0},
		{"CIS_COMPO_bdpm.txt", 8, 32420, 0},
		{"CIS_HAS_SMR_bdpm.txt", 6, 15432, 0},
		{"CIS_HAS_ASMR_bdpm.txt", 6, 10027, 0},
		{"HAS_LiensPageCT_bdpm.txt", 2, 10419, 0},
		{"CIS_GENER_bdpm.txt", 5, 10719, 0},
		{"CIS_CPD_bdpm.txt", 2, 28379, 3},
		{"CIS_CIP_Dispo_Spec.txt", 8, 640, 0},
		{"CIS_MITM.txt", 4, 7711, 0},
	}

	byName := make(map[string]FileDescriptor, len(DefaultFiles()))
	for _, d := range DefaultFiles() {
		byName[d.Name] = d
	}

	var wanted []FileDescriptor
	for _, c := range cases {
		d, ok := byName[c.file]
		if !ok {
			t.Fatalf("%s absent de DefaultFiles()", c.file)
		}
		wanted = append(wanted, d)
	}

	res, err := NewClient("test").FetchAll(ctx, wanted)
	if err != nil {
		t.Fatalf("FetchAll() : %v", err)
	}

	content := make(map[string][]byte, len(res.Files))
	for _, f := range res.Files {
		content[f.Name] = f.Bytes
	}

	for _, c := range cases {
		text, _, err := DecodeFile(content[c.file])
		if err != nil {
			t.Errorf("%s : DecodeFile() : %v", c.file, err)
			continue
		}

		var quarantined int
		sc := NewScanner(c.file, strings.NewReader(text), c.fields,
			func(string, int, string, string) { quarantined++ })

		var records int
		for sc.Scan() {
			records++
		}
		if err := sc.Err(); err != nil {
			t.Errorf("%s : Scan() : %v", c.file, err)
			continue
		}

		// records + quarantined, car une ligne au mauvais nombre de colonnes
		// est écartée du flux mais reste un enregistrement du fichier.
		want := c.lines - c.blank
		if got := records + quarantined; got != want {
			t.Errorf("%s : %d enregistrements (%d retenus, %d en quarantaine), %d attendus "+
				"(%d lignes moins %d sans donnée)",
				c.file, got, records, quarantined, want, c.lines, c.blank)
		}
		if quarantined > 0 {
			t.Logf("%s : %d ligne(s) en quarantaine sur %d", c.file, quarantined, c.lines)
		}
	}
}

// TestLive_ParseAll fait passer les dix fichiers réels dans leur parseur
// respectif (T-08) et vérifie qu'aucune erreur fatale ne survient, que le
// taux global de quarantaine reste sous 1 % et que les volumétries
// mesurées correspondent à docs/01-analyse-source-bdpm.md §2. C'est ce
// test qui prouve le critère d'acceptation « les 10 fichiers réels sont
// parsés intégralement ».
//
// Les valeurs d'énumération jamais observées (statut_amm, procedure_amm,
// etat_commercialisation, statut_bdm, statut_administratif) sont
// journalisées mais ne font pas échouer le test : le référentiel doit
// rester servi même si l'ANSM ajoute une modalité.
func TestLive_ParseAll(t *testing.T) { //nolint:revive // fonction de test longue, délibérément non découpée : elle reflète un seul scénario de bout en bout.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := NewClient("test").FetchAll(ctx, DefaultFiles())
	if err != nil {
		t.Fatalf("FetchAll() : %v", err)
	}

	decoded := make(map[string]string, len(res.Files))
	for _, f := range res.Files {
		text, _, err := DecodeFile(f.Bytes)
		if err != nil {
			t.Fatalf("%s : DecodeFile() : %v", f.Name, err)
		}
		decoded[f.Name] = text
	}

	var totalRecords, totalQuarantined int
	quarantineByReason := make(map[string]int)
	newReader := func(file string) *strings.Reader { return strings.NewReader(decoded[file]) }
	quarantineFor := func(file string) QuarantineFunc {
		return func(_ string, line int, reason, raw string) {
			totalQuarantined++
			quarantineByReason[reason]++
			t.Logf("quarantaine %s:%d %s : %.80q", file, line, reason, raw)
		}
	}
	unknownFor := func(file string) UnknownValueFunc {
		seen := make(map[string]bool)
		return func(_, field, value string) {
			key := field + "=" + value
			if seen[key] {
				return
			}
			seen[key] = true
			t.Logf("valeur inconnue %s : %s = %q", file, field, value)
		}
	}

	specs, err := ParseSpecialites("CIS_bdpm.txt", newReader("CIS_bdpm.txt"),
		quarantineFor("CIS_bdpm.txt"), unknownFor("CIS_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseSpecialites() : %v", err)
	}
	totalRecords += len(specs)

	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", newReader("CIS_CIP_bdpm.txt"),
		quarantineFor("CIS_CIP_bdpm.txt"), unknownFor("CIS_CIP_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParsePresentations() : %v", err)
	}
	totalRecords += len(pres)

	compos, err := ParseComposants("CIS_COMPO_bdpm.txt", newReader("CIS_COMPO_bdpm.txt"),
		quarantineFor("CIS_COMPO_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseComposants() : %v", err)
	}
	totalRecords += len(compos)

	avisSMR, err := ParseAvisSMR("CIS_HAS_SMR_bdpm.txt", newReader("CIS_HAS_SMR_bdpm.txt"),
		quarantineFor("CIS_HAS_SMR_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseAvisSMR() : %v", err)
	}
	totalRecords += len(avisSMR)

	avisASMR, err := ParseAvisASMR("CIS_HAS_ASMR_bdpm.txt", newReader("CIS_HAS_ASMR_bdpm.txt"),
		quarantineFor("CIS_HAS_ASMR_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseAvisASMR() : %v", err)
	}
	totalRecords += len(avisASMR)

	liensCT, err := ParseLiensCT("HAS_LiensPageCT_bdpm.txt", newReader("HAS_LiensPageCT_bdpm.txt"),
		quarantineFor("HAS_LiensPageCT_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseLiensCT() : %v", err)
	}
	totalRecords += len(liensCT)

	appartenances, err := ParseGroupes("CIS_GENER_bdpm.txt", newReader("CIS_GENER_bdpm.txt"),
		quarantineFor("CIS_GENER_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseGroupes() : %v", err)
	}
	totalRecords += len(appartenances)

	conditions, err := ParseConditions("CIS_CPD_bdpm.txt", newReader("CIS_CPD_bdpm.txt"),
		quarantineFor("CIS_CPD_bdpm.txt"))
	if err != nil {
		t.Fatalf("ParseConditions() : %v", err)
	}
	totalRecords += len(conditions)

	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", newReader("CIS_CIP_Dispo_Spec.txt"),
		quarantineFor("CIS_CIP_Dispo_Spec.txt"))
	if err != nil {
		t.Fatalf("ParseRuptures() : %v", err)
	}
	totalRecords += len(ruptures)

	mitm, err := ParseMITM("CIS_MITM.txt", newReader("CIS_MITM.txt"), quarantineFor("CIS_MITM.txt"))
	if err != nil {
		t.Fatalf("ParseMITM() : %v", err)
	}
	totalRecords += len(mitm)

	// Taux de rejet global (docs/09-pipeline-mise-a-jour.md §3.6 : seuil
	// d'ingestion à 1 %, MEDS_MAX_REJECT_RATIO par défaut).
	if totalRecords+totalQuarantined > 0 {
		ratio := float64(totalQuarantined) / float64(totalRecords+totalQuarantined)
		if ratio > 0.01 {
			t.Errorf("taux de quarantaine global = %.4f%%, attendu < 1%% (%v)", ratio*100, quarantineByReason)
		}
		t.Logf("quarantaine globale : %d/%d (%.4f%%) — %v",
			totalQuarantined, totalRecords+totalQuarantined, ratio*100, quarantineByReason)
	}

	// Volumétries mesurées le 14/08/2026 (docs/01-analyse-source-bdpm.md
	// §2). Un écart n'est pas nécessairement un défaut du parseur : la
	// donnée amont évolue d'un jour sur l'autre (l'ANSM publie de
	// nouvelles AMM, retire des présentations…) ; c'est pourquoi la
	// comparaison tolère une marge, sauf pour le nombre de fichiers
	// traités et les invariants structurels (CIP13 tous distincts).
	assertApprox(t, "spécialités", len(specs), 15857)
	assertApprox(t, "présentations", len(pres), 20903)
	assertApprox(t, "composants", len(compos), 32420)
	assertApprox(t, "avis SMR", len(avisSMR), 15432)
	assertApprox(t, "avis ASMR", len(avisASMR), 10027)
	assertApprox(t, "liens CT", len(liensCT), 10419)
	assertApprox(t, "appartenances génériques", len(appartenances), 10719)
	assertApprox(t, "conditions", len(conditions), 28376)
	assertApprox(t, "ruptures", len(ruptures), 640)
	assertApprox(t, "MITM", len(mitm), 7711)

	cip13 := make(map[string]struct{}, len(pres))
	for _, p := range pres {
		cip13[p.CIP13] = struct{}{}
	}
	if len(cip13) != len(pres) {
		t.Errorf("CIP13 distincts = %d, attendu %d (autant que de présentations)", len(cip13), len(pres))
	}

	substances := make(map[string]struct{})
	for _, c := range compos {
		substances[c.CodeSubstance] = struct{}{}
	}
	assertApprox(t, "substances distinctes", len(substances), 3895)

	titulaires := make(map[string]struct{})
	formes := make(map[string]struct{})
	for _, s := range specs {
		for _, tit := range s.Titulaires {
			titulaires[tit] = struct{}{}
		}
		formes[s.FormePharmaceutique] = struct{}{}
	}
	assertApprox(t, "titulaires distincts", len(titulaires), 666)
	assertApprox(t, "formes distinctes", len(formes), 367)

	groupes := make(map[string]struct{})
	for _, a := range appartenances {
		groupes[a.GroupeID] = struct{}{}
	}
	assertApprox(t, "groupes génériques distincts", len(groupes), 1671)
}

// assertApprox compare got à want avec une tolérance de ±20 % (même
// garde-fou de vraisemblance que le pipeline d'ingestion,
// docs/09-pipeline-mise-a-jour.md §3.6) : les volumétries de
// docs/01-analyse-source-bdpm.md §2 datent du 14/08/2026 et la donnée
// amont évolue.
func assertApprox(t *testing.T, label string, got, want int) {
	t.Helper()
	if want == 0 {
		return
	}
	ratio := float64(got) / float64(want)
	if ratio < 0.8 || ratio > 1.2 {
		t.Errorf("%s : %d, attendu %d ±20%%", label, got, want)
	}
	t.Logf("%s : %d (attendu %d)", label, got, want)
}
