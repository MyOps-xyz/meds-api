package bdpm

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// encodeWindows1252 encode s (supposée n'utiliser que des caractères
// représentables en Windows-1252) pour construire des fixtures de test
// réalistes, sans dépendre d'un fichier binaire versionné.
func encodeWindows1252(t *testing.T, s string) []byte {
	t.Helper()
	b, err := charmap.Windows1252.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encodage Windows-1252 de %q échoué : %v", s, err)
	}
	return b
}

func TestDecodeFile_UTF8Content(t *testing.T) {
	t.Parallel()

	raw := []byte("60000001\tcomprimé pelliculé\tUTF-8 déjà valide")
	text, enc, err := DecodeFile(raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	if enc != EncodingUTF8 {
		t.Errorf("encoding = %q, attendu %q", enc, EncodingUTF8)
	}
	if !strings.Contains(text, "comprimé") {
		t.Errorf("texte décodé = %q, devrait contenir \"comprimé\"", text)
	}
}

func TestDecodeFile_Windows1252Content(t *testing.T) {
	t.Parallel()

	raw := encodeWindows1252(t, "60000001\tcomprimé pelliculé")
	text, enc, err := DecodeFile(raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	if enc != EncodingWindows1252 {
		t.Errorf("encoding = %q, attendu %q", enc, EncodingWindows1252)
	}
	if !strings.Contains(text, "comprimé") {
		t.Errorf("texte décodé = %q, devrait contenir \"comprimé\"", text)
	}
}

// TestDecodeFile_TypographicApostrophe couvre explicitement l'octet 0x92,
// l'apostrophe typographique de Windows-1252 (U+2019), qui aurait été un
// caractère de contrôle en Latin-1/ISO-8859-1 strict — la raison précise du
// choix de Windows-1252 plutôt que Latin-1 (docs/01-analyse-source-bdpm.md,
// piège P1).
func TestDecodeFile_TypographicApostrophe(t *testing.T) {
	t.Parallel()

	raw := []byte{'d', 0x92, 'a', 'r', 'r', 'ê', 't'}
	// 0xea = 'ê' en Windows-1252 (identique à Latin-1 pour ce point).
	raw[5] = 0xea

	text, enc, err := DecodeFile(raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	if enc != EncodingWindows1252 {
		t.Fatalf("encoding = %q, attendu %q", enc, EncodingWindows1252)
	}
	want := "d’arrêt" // U+2019 RIGHT SINGLE QUOTATION MARK
	if text != want {
		t.Errorf("texte décodé = %q, attendu %q (0x92 doit devenir U+2019, pas un caractère de contrôle)", text, want)
	}
}

func TestDecodeFile_ASCIIDetectedAsUTF8(t *testing.T) {
	t.Parallel()

	raw := []byte("60000001\tCode dossier HAS\thttp://exemple")
	_, enc, err := DecodeFile(raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	if enc != EncodingUTF8 {
		t.Errorf("encoding = %q, attendu %q (l'ASCII est un sous-ensemble valide d'UTF-8)", enc, EncodingUTF8)
	}
}

func TestDecodeFile_BOMStripped(t *testing.T) {
	t.Parallel()

	raw := append(append([]byte{}, utf8BOM...), []byte("60000001\tdénomination")...)
	text, enc, err := DecodeFile(raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	if enc != EncodingUTF8 {
		t.Errorf("encoding = %q, attendu %q", enc, EncodingUTF8)
	}
	if strings.HasPrefix(text, string(utf8BOM)) {
		t.Fatal("le BOM aurait dû être retiré")
	}
	if !strings.HasPrefix(text, "60000001") {
		t.Errorf("le premier champ devrait commencer directement par le code CIS, obtenu %q", text)
	}
}

func TestDecodeFile_EmptyContentNotFatal(t *testing.T) {
	t.Parallel()

	text, enc, err := DecodeFile(nil)
	if err != nil {
		t.Fatalf("DecodeFile(nil) erreur inattendue : %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, attendu vide", text)
	}
	if enc != EncodingUTF8 {
		t.Errorf("encoding = %q, attendu %q par convention pour un contenu vide", enc, EncodingUTF8)
	}
}

// TestDecodeFile_EncodingSwitchesBetweenCalls prouve qu'un même fichier
// peut basculer d'un encodage à l'autre entre deux appels successifs, sans
// aucun état conservé entre eux (pas de mémorisation par nom de fichier).
func TestDecodeFile_EncodingSwitchesBetweenCalls(t *testing.T) {
	t.Parallel()

	utf8Raw := []byte("comprimé en UTF-8")
	latin1Raw := encodeWindows1252(t, "comprimé en Windows-1252")

	_, enc1, err := DecodeFile(utf8Raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	_, enc2, err := DecodeFile(latin1Raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}
	_, enc3, err := DecodeFile(utf8Raw)
	if err != nil {
		t.Fatalf("DecodeFile() erreur inattendue : %v", err)
	}

	if enc1 != EncodingUTF8 || enc3 != EncodingUTF8 {
		t.Errorf("enc1=%q enc3=%q, attendu %q pour les deux", enc1, enc3, EncodingUTF8)
	}
	if enc2 != EncodingWindows1252 {
		t.Errorf("enc2=%q, attendu %q", enc2, EncodingWindows1252)
	}
}

// TestDecodeFile_RealFileNames vérifie les deux cas d'acceptation cités
// explicitement dans la spécification : CIS_CIP_bdpm.txt (UTF-8) et
// CIS_bdpm.txt (Windows-1252). Le nom de fichier lui-même n'entre dans
// aucune décision de DecodeFile ; ce test documente seulement le résultat
// attendu pour ces deux fichiers avec un contenu représentatif.
func TestDecodeFile_RealFileNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file    string
		raw     []byte
		wantEnc string
	}{
		{
			file:    "CIS_CIP_bdpm.txt",
			raw:     []byte("60000001\t1234567\tboîte de comprimés"),
			wantEnc: EncodingUTF8,
		},
		{
			file:    "CIS_bdpm.txt",
			raw:     encodeWindows1252(t, "60000001\tA 313 200 000 UI, pommade\tcutanée"),
			wantEnc: EncodingWindows1252,
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			text, enc, err := DecodeFile(tt.raw)
			if err != nil {
				t.Fatalf("DecodeFile(%s) erreur inattendue : %v", tt.file, err)
			}
			if enc != tt.wantEnc {
				t.Errorf("%s: encoding = %q, attendu %q", tt.file, enc, tt.wantEnc)
			}
			if !strings.Contains(text, "comprimé") && !strings.Contains(text, "pommade") {
				t.Errorf("%s: texte décodé suspect : %q", tt.file, text)
			}
		})
	}
}
