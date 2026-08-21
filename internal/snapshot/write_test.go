package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 15, 4, 12, 33, 0, time.UTC)
	return func() time.Time { return t }
}

func sampleDataset() *bdpm.Dataset {
	prix := int32(2434)
	return &bdpm.Dataset{
		Specialites: []bdpm.Specialite{
			{CIS: "60002283", Denomination: "DOLIPRANE 500 mg, comprimé",
				VoiesAdministration: []string{"orale"}, Titulaires: []string{"OPELLA"}},
			{CIS: "60002284", Denomination: "ACIDE ACÉTYLSALICYLIQUE & VITAMINE C"},
		},
		Presentations: []bdpm.Presentation{
			{CIP13: "3400930000011", CIP7: "3000001", CIS: "60002283",
				Libelle: "plaquette de 16", PrixMedicamentCents: &prix},
		},
		Composants: []bdpm.Composant{
			{CIS: "60002283", CodeSubstance: "42215", DenominationSubstance: "PARACÉTAMOL",
				Dosage: "500 mg", Nature: "SA", NumeroLiaison: 1},
		},
		MITM: []bdpm.InfoMITM{{CIS: "60002283", CodeATC: "N02BE01"}},
	}
}

func writeSample(t *testing.T, dir string, opts WriteOptions) (*Result, *bdpm.Quarantine) {
	t.Helper()
	d := sampleDataset()
	d.Derive()
	q := bdpm.NewQuarantine()
	if opts.DataDir == "" {
		opts.DataDir = dir
	}
	if opts.Now == nil {
		opts.Now = fixedClock()
	}
	res, err := Write(d, q, opts)
	if err != nil {
		t.Fatalf("Write : %v", err)
	}
	return res, q
}

func TestWrite_ManifestEcritEnDernier(t *testing.T) {
	dir := t.TempDir()
	res, _ := writeSample(t, dir, WriteOptions{Hash: "abc123", Generator: "test/1"})

	entries, err := os.ReadDir(res.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 11 {
		t.Fatalf("%d fichiers écrits, veut 11 (10 entités + manifest)", len(entries))
	}

	var manifestMod, latestEntity time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == ManifestFile {
			manifestMod = info.ModTime()
			continue
		}
		if info.ModTime().After(latestEntity) {
			latestEntity = info.ModTime()
		}
	}
	if manifestMod.Before(latestEntity) {
		t.Errorf("le manifest (%v) précède une entité (%v) : sa présence n'atteste plus la complétude",
			manifestMod, latestEntity)
	}
}

// Critère d'acceptation T-11 : deux exécutions sur la même entrée produisent
// des fichiers identiques octet pour octet.
func TestWrite_DeterministeOctetPourOctet(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	resA, _ := writeSample(t, dirA, WriteOptions{Hash: "abc123", Generator: "test/1"})
	resB, _ := writeSample(t, dirB, WriteOptions{Hash: "abc123", Generator: "test/1"})

	for _, name := range []string{
		SpecialitesFile, PresentationsFile, ComposantsFile, SubstancesFile,
		GroupesFile, AvisSMRFile, AvisASMRFile, ConditionsFile, RupturesFile, MITMFile,
		ManifestFile,
	} {
		a, err := os.ReadFile(filepath.Join(resA.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(resB.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s diffère entre deux exécutions sur la même entrée", name)
		}
	}
}

// Les libellés de la BDPM contiennent des « & » : l'échappement HTML par
// défaut de encoding/json les rendrait illisibles.
func TestWrite_PasDEchappementHTML(t *testing.T) {
	dir := t.TempDir()
	res, _ := writeSample(t, dir, WriteOptions{Hash: "abc123"})

	b, err := os.ReadFile(filepath.Join(res.Dir, SpecialitesFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`\u0026`)) {
		t.Errorf("le « & » a été échappé en \\u0026 dans le NDJSON")
	}
	if !bytes.Contains(b, []byte("ACIDE ACÉTYLSALICYLIQUE & VITAMINE C")) {
		t.Errorf("le libellé n'est pas restitué littéralement")
	}
}

func TestWrite_CurrentPointeVersLeSnapshot(t *testing.T) {
	dir := t.TempDir()
	res, _ := writeSample(t, dir, WriteOptions{Hash: "abc123"})

	cur, err := Current(dir)
	if err != nil {
		t.Fatalf("Current : %v", err)
	}
	if cur != res.Dir {
		t.Errorf("current = %s, veut %s", cur, res.Dir)
	}

	// La cible du lien est relative : le répertoire de données reste
	// déplaçable, ce qui est indispensable sous conteneur.
	target, err := os.Readlink(filepath.Join(dir, SnapshotsDir, CurrentLink))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("le lien courant est absolu (%s) : le répertoire ne serait plus déplaçable", target)
	}
}

func TestCurrent_SansSnapshot(t *testing.T) {
	if _, err := Current(t.TempDir()); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("err = %v, veut ErrNoSnapshot : un démarrage à froid n'est pas une anomalie", err)
	}
}

// Critère d'acceptation T-11 : `current` pointe toujours vers un snapshot
// valide, même si une écriture est interrompue.
//
// L'interruption est simulée en fabriquant le répertoire temporaire qu'une
// écriture avortée aurait laissé : c'est exactement l'état sur disque après
// un SIGKILL en cours d'écriture.
func TestWrite_EcritureInterrompueNAffectePasCurrent(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir, WriteOptions{Hash: "premier"})

	root := filepath.Join(dir, SnapshotsDir)
	orphan := filepath.Join(root, tmpPrefix+"second")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, SpecialitesFile), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cur, err := Current(dir)
	if err != nil {
		t.Fatalf("Current après interruption : %v", err)
	}
	if filepath.Base(cur) != "premier" {
		t.Errorf("current = %s, veut le snapshot complet précédent", cur)
	}

	// Le résidu est purgé à l'écriture suivante.
	writeSample(t, dir, WriteOptions{Hash: "troisieme"})
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("le répertoire temporaire orphelin n'a pas été purgé")
	}
}

func TestPurgeTemp_AuDemarrage(t *testing.T) {
	dir := t.TempDir()
	writeSample(t, dir, WriteOptions{Hash: "abc123"})

	root := filepath.Join(dir, SnapshotsDir)
	orphan := filepath.Join(root, tmpPrefix+"avorte")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PurgeTemp(dir); err != nil {
		t.Fatalf("PurgeTemp : %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("le résidu n'a pas été purgé")
	}
	// Le snapshot publié et son lien survivent.
	if _, err := Current(dir); err != nil {
		t.Errorf("PurgeTemp a endommagé le snapshot courant : %v", err)
	}
}

func TestPurgeTemp_RepertoireInexistant(t *testing.T) {
	if err := PurgeTemp(filepath.Join(t.TempDir(), "jamais-cree")); err != nil {
		t.Fatalf("PurgeTemp sur un répertoire vierge doit être un no-op : %v", err)
	}
}

func TestWrite_PurgeLesAnciens(t *testing.T) {
	dir := t.TempDir()
	hashes := []string{"h1", "h2", "h3", "h4", "h5"}
	for i, h := range hashes {
		writeSample(t, dir, WriteOptions{Hash: h, Keep: 3})
		// Les dates de modification servent au tri : un tick suffit à les
		// ordonner de façon fiable sur tout système de fichiers.
		if i < len(hashes)-1 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	root := filepath.Join(dir, SnapshotsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 3 {
		t.Fatalf("%v conservés, veut les 3 plus récents", dirs)
	}
	for _, d := range dirs {
		if d == "h1" || d == "h2" {
			t.Errorf("%s aurait dû être purgé", d)
		}
	}
	// Le courant est toujours résolvable après purge.
	cur, err := Current(dir)
	if err != nil || filepath.Base(cur) != "h5" {
		t.Errorf("current = %s (%v), veut h5", cur, err)
	}
}

func TestWrite_KeepZeroDesactiveLaPurge(t *testing.T) {
	dir := t.TempDir()
	for _, h := range []string{"h1", "h2", "h3"} {
		writeSample(t, dir, WriteOptions{Hash: h, Keep: 0})
	}
	entries, _ := os.ReadDir(filepath.Join(dir, SnapshotsDir))
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d snapshots, veut 3 : Keep=0 désactive la purge", n)
	}
}

func TestWrite_HashManquant(t *testing.T) {
	d := sampleDataset()
	if _, err := Write(d, bdpm.NewQuarantine(), WriteOptions{DataDir: t.TempDir()}); err == nil {
		t.Fatal("Write sans hash doit échouer")
	}
}

func TestWrite_ManifestComplet(t *testing.T) {
	dir := t.TempDir()
	d := sampleDataset()
	d.Derive()
	q := bdpm.NewQuarantine()
	q.Add(bdpm.FilePresentations, 12, bdpm.ReasonPrixInvalide, "brut")
	q.AddOutOfScope(bdpm.FileAvisSMR, 5, bdpm.ReasonCISOrphelin, "brut")
	q.AddUnknown(bdpm.FileSpecialites, "statut_amm", "Statut inédit")

	res, err := Write(d, q, WriteOptions{
		DataDir: dir, Hash: "abc123", PreviousHash: "def456",
		Generator: "bdpm-sync/1.0.0", Now: fixedClock(),
	})
	if err != nil {
		t.Fatal(err)
	}

	m, err := ReadManifest(res.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hash != "abc123" || m.PreviousHash != "def456" {
		t.Errorf("hash = %s / précédent = %s", m.Hash, m.PreviousHash)
	}
	if m.Version != "2026-08-15T04:12:33Z" {
		t.Errorf("version = %s", m.Version)
	}
	if m.Generator != "bdpm-sync/1.0.0" {
		t.Errorf("generator = %s", m.Generator)
	}
	if m.Counts["specialites"] != 2 || m.Counts["substances"] != 1 {
		t.Errorf("compteurs = %+v", m.Counts)
	}
	// Les deux catégories sont bien séparées dans le manifest (ADR 0007).
	if m.Quarantine.Total != 1 || m.Quarantine.ByReason[bdpm.ReasonPrixInvalide] != 1 {
		t.Errorf("quarantaine = %+v", m.Quarantine)
	}
	if m.OutOfScope.Total != 1 || m.OutOfScope.ByReason[bdpm.ReasonCISOrphelin] != 1 {
		t.Errorf("hors périmètre = %+v", m.OutOfScope)
	}
	if m.Quarantine.RejectRatio == 0 {
		t.Errorf("le taux de rejet doit être renseigné")
	}
	if len(m.UnknownValues) != 1 || m.UnknownValues[0].Value != "Statut inédit" {
		t.Errorf("valeurs inconnues = %+v", m.UnknownValues)
	}
	if len(m.Warnings) != 3 {
		t.Errorf("avertissements = %v, veut rejet + hors périmètre + valeur inconnue", m.Warnings)
	}
}

func TestLoad_AllerRetour(t *testing.T) {
	dir := t.TempDir()
	res, _ := writeSample(t, dir, WriteOptions{Hash: "abc123"})

	d, m, err := Load(res.Dir)
	if err != nil {
		t.Fatalf("Load : %v", err)
	}
	if m.Hash != "abc123" {
		t.Errorf("hash = %s", m.Hash)
	}
	if len(d.Specialites) != 2 || d.Specialites[0].CIS != "60002283" {
		t.Errorf("spécialités = %+v", d.Specialites)
	}
	if len(d.Substances) != 1 || d.Substances[0].Denomination != "PARACÉTAMOL" {
		t.Errorf("substances = %+v", d.Substances)
	}
	// Le prix pointeur survit à l'aller-retour, et un prix absent reste nil.
	if d.Presentations[0].PrixMedicamentCents == nil || *d.Presentations[0].PrixMedicamentCents != 2434 {
		t.Errorf("prix = %v", d.Presentations[0].PrixMedicamentCents)
	}
	if d.Presentations[0].PrixPublicCents != nil {
		t.Errorf("un prix absent doit rester nil, pas 0")
	}
}

func TestLoad_ManifestIncoherent(t *testing.T) {
	dir := t.TempDir()
	res, _ := writeSample(t, dir, WriteOptions{Hash: "abc123"})

	m, err := ReadManifest(res.Dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Counts["specialites"] = 9999
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(res.Dir, ManifestFile), b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Load(res.Dir); err == nil || !strings.Contains(err.Error(), "incohérent") {
		t.Fatalf("err = %v, veut un refus explicite d'un snapshot tronqué", err)
	}
}

func TestReadManifest_Absent(t *testing.T) {
	if _, err := ReadManifest(t.TempDir()); err == nil {
		t.Fatal("un répertoire sans manifest doit être refusé")
	}
}
