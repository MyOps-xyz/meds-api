package snapshot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

// ReadNDJSON charge un fichier NDJSON du snapshot.
//
// Le décodage est incrémental (json.Decoder sur un bufio.Reader) : le plus
// gros fichier du jeu — composants.ndjson, ~32 000 lignes — n'est jamais
// entièrement matérialisé en mémoire sous forme d'octets bruts en plus des
// structures décodées.
// hint est le nombre d'enregistrements attendu, tiré du manifest : il évite
// la dizaine de réallocations-recopies que coûterait la croissance depuis nil
// sur les fichiers volumineux. Un hint faux ou nul reste correct, append
// prenant le relais.
func ReadNDJSON[T any](path string, hint int) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ouverture de %s : %w", path, err)
	}
	// Lecture seule : une erreur de fermeture n'est pas actionnable et ne
	// remet pas en cause les données déjà lues.
	defer func() { _ = f.Close() }()

	br := bufio.NewReaderSize(f, 256*1024)
	dec := json.NewDecoder(br)

	out := make([]T, 0, hint)
	for {
		var v T
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("lecture de %s (enregistrement %d) : %w", path, len(out)+1, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Load charge l'intégralité d'un snapshot en mémoire, sous la forme du
// Dataset qui a servi à l'écrire.
//
// C'est la fonction que la construction du Store (T-17) consomme. Les
// entités dérivées sont relues telles quelles plutôt que reconstruites :
// le snapshot fait autorité, et rejouer la dérivation au chargement
// introduirait un risque de divergence entre ce qui est servi et ce qui est
// écrit.
func Load(dir string) (*bdpm.Dataset, *Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, nil, err
	}

	d := &bdpm.Dataset{SourceFiles: m.SourceFiles}
	if d.Specialites, err = ReadNDJSON[bdpm.Specialite](filepath.Join(dir, SpecialitesFile), m.Counts["specialites"]); err != nil {
		return nil, nil, err
	}
	if d.Presentations, err = ReadNDJSON[bdpm.Presentation](filepath.Join(dir, PresentationsFile), m.Counts["presentations"]); err != nil {
		return nil, nil, err
	}
	if d.Composants, err = ReadNDJSON[bdpm.Composant](filepath.Join(dir, ComposantsFile), m.Counts["composants"]); err != nil {
		return nil, nil, err
	}
	if d.Substances, err = ReadNDJSON[bdpm.Substance](filepath.Join(dir, SubstancesFile), m.Counts["substances"]); err != nil {
		return nil, nil, err
	}
	if d.Groupes, err = ReadNDJSON[bdpm.GroupeGenerique](filepath.Join(dir, GroupesFile), m.Counts["groupes_generiques"]); err != nil {
		return nil, nil, err
	}
	if d.AvisSMR, err = ReadNDJSON[bdpm.AvisSMR](filepath.Join(dir, AvisSMRFile), m.Counts["avis_smr"]); err != nil {
		return nil, nil, err
	}
	if d.AvisASMR, err = ReadNDJSON[bdpm.AvisASMR](filepath.Join(dir, AvisASMRFile), m.Counts["avis_asmr"]); err != nil {
		return nil, nil, err
	}
	if d.Conditions, err = ReadNDJSON[bdpm.Condition](filepath.Join(dir, ConditionsFile), m.Counts["conditions"]); err != nil {
		return nil, nil, err
	}
	if d.Ruptures, err = ReadNDJSON[bdpm.Rupture](filepath.Join(dir, RupturesFile), m.Counts["ruptures"]); err != nil {
		return nil, nil, err
	}
	if d.MITM, err = ReadNDJSON[bdpm.InfoMITM](filepath.Join(dir, MITMFile), m.Counts["mitm"]); err != nil {
		return nil, nil, err
	}

	// Le manifest fait foi : un écart entre ses compteurs et ce qui est
	// effectivement relu signale un snapshot corrompu ou tronqué, qu'il vaut
	// mieux refuser bruyamment que servir à moitié.
	for entity, expected := range m.Counts {
		got, ok := d.Counts()[entity]
		if ok && got != expected {
			return nil, nil, fmt.Errorf(
				"snapshot %s incohérent : %s annonce %d enregistrements, %d relus",
				dir, entity, expected, got)
		}
	}
	return d, m, nil
}
