package snapshot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

// WriteOptions paramètre l'écriture d'un snapshot.
type WriteOptions struct {
	// DataDir est la racine de données (MEDS_DATA_DIR). Les snapshots sont
	// écrits dans son sous-répertoire snapshots/.
	DataDir string
	// Generator identifie le producteur, inscrit au manifest
	// (ex. "bdpm-sync/1.0.0").
	Generator string
	// Hash est le hash global du jeu de données ; il nomme le répertoire.
	Hash string
	// PreviousHash est le hash du snapshot précédent, s'il y en a un.
	PreviousHash string
	// Now permet de figer l'horloge dans les tests. Nil ⇒ time.Now.
	Now func() time.Time
	// Keep est le nombre de snapshots conservés (MEDS_SNAPSHOT_KEEP).
	// Une valeur <= 0 désactive la purge.
	Keep int
}

// Result décrit un snapshot écrit.
type Result struct {
	// Dir est le répertoire final du snapshot.
	Dir string
	// Manifest est le manifest écrit.
	Manifest *Manifest
	// Purged liste les répertoires supprimés par la purge.
	Purged []string
}

// Write sérialise un Dataset en snapshot, atomiquement.
//
// L'ordre des opérations est ce qui porte la garantie : tout est écrit dans
// un répertoire temporaire, le manifest en dernier, puis le répertoire est
// renommé d'un seul appel os.Rename — atomique sur un même système de
// fichiers — et enfin le lien `current` est basculé, lui aussi
// atomiquement (écriture d'un lien temporaire puis rename).
//
// À aucun instant, y compris en cas de coupure brutale du processus,
// `current` ne peut pointer vers un snapshot incomplet.
func Write(d *bdpm.Dataset, q *bdpm.Quarantine, opts WriteOptions) (*Result, error) {
	if opts.Hash == "" {
		return nil, errors.New("snapshot : hash global manquant")
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	root := filepath.Join(opts.DataDir, SnapshotsDir)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("création de %s : %w", root, err)
	}

	// Les résidus d'une écriture interrompue sont purgés avant d'en
	// commencer une nouvelle, pour que le répertoire de données ne grossisse
	// pas indéfiniment après une série de coupures.
	if err := purgeTemp(root); err != nil {
		return nil, err
	}

	final := filepath.Join(root, opts.Hash)
	tmp := filepath.Join(root, tmpPrefix+opts.Hash)
	if err := os.RemoveAll(tmp); err != nil {
		return nil, fmt.Errorf("nettoyage de %s : %w", tmp, err)
	}
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return nil, fmt.Errorf("création de %s : %w", tmp, err)
	}
	// En cas d'échec en cours de route, le temporaire ne doit pas subsister.
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(tmp)
		}
	}()

	if err := writeEntities(tmp, d); err != nil {
		return nil, err
	}

	ts := now().UTC().Format(time.RFC3339)
	m := &Manifest{
		Version:       ts,
		Hash:          opts.Hash,
		PreviousHash:  opts.PreviousHash,
		GeneratedAt:   ts,
		Generator:     opts.Generator,
		SourceFiles:   d.SourceFiles,
		Counts:        d.Counts(),
		Warnings:      bdpm.Warnings(q),
		UnknownValues: q.UnknownValues(),
		Quarantine: QuarantineSummary{
			Total:       q.Total(),
			ByFile:      q.ByFile(),
			ByReason:    q.ByReason(),
			RejectRatio: bdpm.RejectRatio(d, q),
		},
		OutOfScope: OutOfScopeSummary{
			Total:    q.OutOfScopeTotal(),
			ByFile:   q.OutOfScopeByFile(),
			ByReason: q.OutOfScopeByReason(),
			Ratio:    bdpm.OutOfScopeRatio(d, q),
		},
	}
	// Le manifest est écrit en dernier : sa présence atteste la complétude.
	if err := writeJSONFile(filepath.Join(tmp, ManifestFile), m); err != nil {
		return nil, err
	}

	// Un snapshot de même hash existe déjà (même source, réingestion
	// forcée) : il est remplacé, os.Rename refusant d'écraser un répertoire
	// non vide.
	if err := os.RemoveAll(final); err != nil {
		return nil, fmt.Errorf("remplacement de %s : %w", final, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, fmt.Errorf("publication de %s : %w", final, err)
	}
	success = true

	if err := swapCurrent(root, opts.Hash); err != nil {
		return nil, err
	}

	purged, err := purgeOld(root, opts.Keep, opts.Hash)
	if err != nil {
		return nil, err
	}
	return &Result{Dir: final, Manifest: m, Purged: purged}, nil
}

func writeEntities(dir string, d *bdpm.Dataset) error {
	// L'ordre d'écriture n'a pas d'importance fonctionnelle, mais rester
	// déterministe facilite la lecture d'une trace d'exécution.
	if err := writeNDJSON(filepath.Join(dir, SpecialitesFile), d.Specialites); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, PresentationsFile), d.Presentations); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, ComposantsFile), d.Composants); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, SubstancesFile), d.Substances); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, GroupesFile), d.Groupes); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, AvisSMRFile), d.AvisSMR); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, AvisASMRFile), d.AvisASMR); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, ConditionsFile), d.Conditions); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, RupturesFile), d.Ruptures); err != nil {
		return err
	}
	return writeNDJSON(filepath.Join(dir, MITMFile), d.MITM)
}

// writeNDJSON écrit une entité par ligne.
//
// SetEscapeHTML(false) est indispensable : les libellés de la BDPM
// contiennent des « & » (« ACIDE ACÉTYLSALICYLIQUE & VITAMINE C ») que
// l'échappement par défaut transformerait en \\u0026, illisible pour un
// humain comme pour un intégrateur, sans le moindre bénéfice hors contexte
// HTML.
//
// La sortie est déterministe : les entités sont des structures, donc à
// ordre de champs fixe, et aucune carte n'y figure. Deux exécutions sur la
// même entrée produisent des fichiers identiques octet pour octet.
func writeNDJSON[T any](path string, items []T) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("création de %s : %w", path, err)
	}
	// Filet de sécurité en cas de retour anticipé ; la fermeture nominale est
	// explicite et vérifiée en fin de fonction.
	defer func() { _ = f.Close() }()

	bw := bufio.NewWriterSize(f, 256*1024)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)

	for i := range items {
		if err := enc.Encode(items[i]); err != nil {
			return fmt.Errorf("écriture de %s : %w", path, err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("écriture de %s : %w", path, err)
	}
	// Sync avant le rename : sans cela, un arrêt brutal de la machine
	// pourrait laisser un répertoire renommé dont le contenu n'a pas atteint
	// le disque.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("synchronisation de %s : %w", path, err)
	}
	// La fermeture est vérifiée : sur un chemin d'écriture, une erreur de
	// Close signale une écriture incomplète. L'ignorer laisserait le rename
	// publier un snapshot tronqué comme s'il était valide.
	if err := f.Close(); err != nil {
		return fmt.Errorf("fermeture de %s : %w", path, err)
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("création de %s : %w", path, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("écriture de %s : %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("synchronisation de %s : %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fermeture de %s : %w", path, err)
	}
	return nil
}

// swapCurrent fait pointer `current` vers hash, atomiquement.
//
// os.Symlink échoue si la cible existe : le lien est donc créé sous un nom
// temporaire puis renommé par-dessus l'ancien. os.Rename sur un lien
// symbolique remplace l'entrée de répertoire en une opération — un lecteur
// concurrent voit soit l'ancien lien, soit le nouveau, jamais rien d'autre.
//
// La cible est relative (le seul nom du répertoire) : le répertoire de
// données reste ainsi déplaçable et montable à un autre point dans un
// conteneur, sans casser le lien.
func swapCurrent(root, hash string) error {
	link := filepath.Join(root, CurrentLink)
	tmpLink := filepath.Join(root, tmpPrefix+CurrentLink)

	if err := os.Remove(tmpLink); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("nettoyage du lien temporaire : %w", err)
	}
	if err := os.Symlink(hash, tmpLink); err != nil {
		return fmt.Errorf("création du lien courant : %w", err)
	}
	if err := os.Rename(tmpLink, link); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("bascule du lien courant : %w", err)
	}
	return nil
}

// Current résout le lien `current` et retourne le répertoire du snapshot en
// service. L'absence de lien n'est pas une erreur exceptionnelle : c'est
// l'état normal d'un démarrage à froid, signalé par ErrNoSnapshot.
func Current(dataDir string) (string, error) {
	root := filepath.Join(dataDir, SnapshotsDir)
	link := filepath.Join(root, CurrentLink)

	target, err := os.Readlink(link)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoSnapshot
		}
		return "", fmt.Errorf("lecture du lien courant : %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	if _, err := os.Stat(filepath.Join(target, ManifestFile)); err != nil {
		return "", fmt.Errorf("le lien courant pointe vers un snapshot incomplet (%s) : %w", target, err)
	}
	return target, nil
}

// ErrNoSnapshot signale qu'aucun snapshot n'est publié. C'est l'état
// attendu au tout premier démarrage : /readyz répond 503 et une
// synchronisation est déclenchée, sans que ce soit une erreur.
var ErrNoSnapshot = errors.New("aucun snapshot publié")

// purgeTemp supprime les répertoires d'écriture interrompue.
func purgeTemp(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", root, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, tmpPrefix) || name == tmpPrefix+CurrentLink {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("purge de %s : %w", name, err)
		}
	}
	return nil
}

// PurgeTemp expose la purge des résidus pour le démarrage du serveur, où
// elle a lieu avant toute ingestion.
func PurgeTemp(dataDir string) error {
	root := filepath.Join(dataDir, SnapshotsDir)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	return purgeTemp(root)
}

// purgeOld ne conserve que les keep snapshots les plus récents, le snapshot
// courant étant toujours préservé quel que soit son rang.
//
// Le tri se fait sur la date de modification du répertoire, pas sur le nom :
// le hash n'a aucun ordre chronologique.
func purgeOld(root string, keep int, current string) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", root, err)
	}

	type snap struct {
		name string
		mod  time.Time
	}
	var snaps []snap
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		snaps = append(snaps, snap{name: e.Name(), mod: info.ModTime()})
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].mod.Equal(snaps[j].mod) {
			return snaps[i].mod.After(snaps[j].mod)
		}
		return snaps[i].name < snaps[j].name
	})

	var purged []string
	kept := 0
	for _, s := range snaps {
		if s.name == current {
			kept++
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, s.name)); err != nil {
			return purged, fmt.Errorf("purge de %s : %w", s.name, err)
		}
		purged = append(purged, s.name)
	}
	return purged, nil
}
