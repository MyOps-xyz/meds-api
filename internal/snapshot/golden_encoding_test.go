package snapshot

import (
	"bytes"
	"io"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// encodeWindows1252Bytes encode du texte UTF-8 en Windows-1252.
//
// Les échantillons figés doivent être écrits dans l'encodage réel de la
// source, sans quoi ils ne prouveraient rien du décodage. Cette fonction
// n'existe donc que pour la génération des golden files : le code de
// production ne fait jamais que décoder.
func encodeWindows1252Bytes(s string) ([]byte, error) {
	var buf bytes.Buffer
	w := transform.NewWriter(&buf, charmap.Windows1252.NewEncoder())
	if _, err := io.WriteString(w, s); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
