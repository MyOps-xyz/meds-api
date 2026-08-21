package bdpm

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Noms d'encodage inscrits au manifest (docs/09-pipeline-mise-a-jour.md
// §3.3). Ce sont des chaînes stables, pas des identifiants Go internes :
// elles apparaissent telles quelles dans manifest.json et les journaux.
const (
	EncodingUTF8        = "utf-8"
	EncodingWindows1252 = "windows-1252"
)

// utf8BOM est le marqueur d'ordre des octets UTF-8 (EF BB BF). Sa présence
// en tête de fichier n'a aucune valeur sémantique en TSV : non retiré, il
// se retrouve concaténé au premier champ du premier enregistrement (le
// code CIS), le rendant inexploitable pour toute comparaison ou jointure.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// DecodeFile détecte l'encodage d'un contenu brut de fichier BDPM et le
// décode en texte UTF-8 exploitable par le reste du pipeline.
//
// La détection est refaite à chaque appel, jamais mémorisée ni codée en
// dur par nom de fichier : l'ANSM sert huit des dix fichiers accentués en
// Windows-1252 et un neuvième (CIS_CIP_bdpm.txt) en UTF-8, et migre
// visiblement fichier par fichier (docs/01-analyse-source-bdpm.md, piège
// P1). Un même nom de fichier peut donc changer d'encodage d'une
// synchronisation à l'autre sans préavis ; seule une détection par contenu,
// systématique, absorbe cette bascule sans intervention.
//
// Règle : un contenu UTF-8 valide (utf8.Valid) est pris tel quel. Sinon, il
// est décodé en Windows-1252 plutôt qu'en ISO-8859-1/Latin-1 strict : c'est
// un sur-ensemble qui couvre en plus les guillemets typographiques et le
// tiret cadratin de la plage 0x80–0x9F, là où Latin-1 ne place que des
// caractères de contrôle à ces positions — une différence directement
// visible sur les libellés français de la BDPM.
//
// L'encodage retenu est retourné en second résultat pour être inscrit au
// manifest : une bascule de l'ANSM vers l'UTF-8 doit devenir visible et
// traçable, pas silencieuse (docs/09-pipeline-mise-a-jour.md §3.3).
func DecodeFile(raw []byte) (text string, encoding string, err error) {
	raw = bytes.TrimPrefix(raw, utf8BOM)

	if len(raw) == 0 {
		// Un fichier vide n'est pas une erreur de décodage : c'est une
		// anomalie de contenu, traitée par la validation de vraisemblance
		// (T-09), pas par ce paquet.
		return "", EncodingUTF8, nil
	}

	if utf8.Valid(raw) {
		return string(raw), EncodingUTF8, nil
	}

	decoded, err := charmap.Windows1252.NewDecoder().Bytes(raw)
	if err != nil {
		return "", "", fmt.Errorf("décodage Windows-1252 échoué : %w", err)
	}
	return string(decoded), EncodingWindows1252, nil
}
