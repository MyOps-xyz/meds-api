package bdpm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Le fuzzing vise ici une propriété unique et non négociable : **aucune
// entrée, si hostile soit-elle, ne doit faire paniquer un parseur**.
//
// C'est la garantie qui compte pour ce projet : les fichiers viennent d'un
// tiers, sont récupérés sans authentification, et une panique à l'ingestion
// mettrait fin au processus qui sert l'API. La quarantaine est le
// comportement attendu face à une ligne incompréhensible ; la panique ne
// l'est jamais.
//
//	go test -run xxx -fuzz FuzzParse -fuzztime 60s ./internal/bdpm/
//
// Les entrées hors du domaine utile sont écartées par un simple `return` et
// non par t.Skip, que la documentation de Go déconseille depuis une cible de
// fuzz : un cas ignoré n'entre pas au corpus.
//
// Note d'exploitation : le moteur de fuzzing affiche par intermittence des
// paliers à « 0 exec/sec » de trois à six secondes sur cette machine. Ce
// n'est pas un blocage du code — mesuré sur les 105 entrées du corpus, le
// pire cas de Normalize est de 115 µs, et aucune entrée construite à la main
// (UTF-8 invalide, marques combinantes en série, 100 Kio d'accents) ne
// dépasse la milliseconde. C'est un artefact du moteur avec dix workers ;
// prévoir une marge sur -fuzztime en intégration continue plutôt que de
// chercher une pathologie qui n'existe pas.

// seedLines fournit des amorces représentatives, dont les huit pièges
// documentés : encodages, terminaisons, guillemets nus, valeurs limites.
var seedLines = []string{
	"",
	"\n",
	"\r\n",
	"\t",
	"60002283\tDOLIPRANE 1000 mg, comprimé\tcomprimé\torale\tAutorisation active\tProcédure nationale\tCommercialisée\t19/03/1997\t\t\tOPELLA\tNon",
	"60002283\t3000001\tplaquette\tPrésentation active\tDéclaration de commercialisation\t16/03/2011\t3400930000011\toui\t65 %\t1,78\t2,80\t1,02\t",
	// P8 : guillemet nu, qu'encoding/csv interpréterait à tort.
	`60002283	prescription réservée aux "spécialistes"`,
	// P4 : séparateur de milliers dans un prix.
	"60002283\t3000001\tx\ty\tz\t16/03/2011\t3400930000011\toui\t65 %\t1 234,56\t1 500,00\t1,02\t",
	// Valeurs limites et absurdes.
	"99999999999999999999\tx",
	"-1\t-1\t-1",
	"\x00\x01\x02\tbinaire",
	strings.Repeat("a", 100000) + "\tb",
	strings.Repeat("\t", 1000),
	"60002283\t" + strings.Repeat("é", 10000),
	// UTF-8 invalide : le décodeur doit s'en accommoder.
	"60002283\t\xff\xfe\xfd",
}

// parsers énumère les dix parseurs, chacun testé sur la même entrée : un
// fichier peut toujours être servi à la place d'un autre par une source
// défaillante, et aucun ne doit paniquer pour autant.
var parsers = []struct {
	name string
	fn   func(string, *strings.Reader, QuarantineFunc, UnknownValueFunc) error
}{
	{"specialites", func(f string, r *strings.Reader, q QuarantineFunc, u UnknownValueFunc) error {
		_, err := ParseSpecialites(f, r, q, u)
		return err
	}},
	{"presentations", func(f string, r *strings.Reader, q QuarantineFunc, u UnknownValueFunc) error {
		_, err := ParsePresentations(f, r, q, u)
		return err
	}},
	{"composants", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseComposants(f, r, q)
		return err
	}},
	{"avis_smr", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseAvisSMR(f, r, q)
		return err
	}},
	{"avis_asmr", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseAvisASMR(f, r, q)
		return err
	}},
	{"liens_ct", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseLiensCT(f, r, q)
		return err
	}},
	{"groupes", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseGroupes(f, r, q)
		return err
	}},
	{"conditions", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseConditions(f, r, q)
		return err
	}},
	{"ruptures", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseRuptures(f, r, q)
		return err
	}},
	{"mitm", func(f string, r *strings.Reader, q QuarantineFunc, _ UnknownValueFunc) error {
		_, err := ParseMITM(f, r, q)
		return err
	}},
}

// FuzzParseTousLesFichiers soumet la même entrée aux dix parseurs.
func FuzzParseTousLesFichiers(f *testing.F) {
	for _, seed := range seedLines {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Une entrée démesurée ferait durer le fuzzing sans rien apprendre :
		// la borne de taille est déjà couverte par MaxLineSize du Scanner.
		if len(input) > 1<<20 {
			return
		}
		for _, p := range parsers {
			q := NewQuarantine()
			// Une erreur est acceptable, une panique ne l'est pas : c'est la
			// seule assertion, et elle tient dans l'absence de recover.
			_ = p.fn(p.name, strings.NewReader(input), q.Add, q.AddUnknown)
		}
	})
}

// FuzzDecodeFile vise la détection d'encodage : elle reçoit des octets bruts
// et doit toujours produire de l'UTF-8 valide, quelle que soit l'entrée.
func FuzzDecodeFile(f *testing.F) {
	f.Add([]byte("comprimé pelliculé"))
	f.Add([]byte{0xEF, 0xBB, 0xBF, 'a', 'b'}) // BOM UTF-8
	f.Add([]byte{0xE9, 0x20, 0xE8})           // Latin-1 pur
	f.Add([]byte{0xFF, 0xFE, 0x00, 0x41})     // UTF-16 LE, non supporté
	f.Add([]byte{0xC3, 0xA9})                 // é en UTF-8
	f.Add([]byte{0xC3})                       // séquence UTF-8 tronquée
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			return
		}
		text, enc, err := DecodeFile(raw)
		if err != nil {
			return
		}
		// La propriété : quelle que soit l'entrée, la sortie est de l'UTF-8
		// valide. Sans elle, le JSON produit en aval serait invalide, et
		// l'API servirait des octets qu'aucun client ne saurait lire.
		if !utf8.ValidString(text) {
			t.Fatalf("DecodeFile a produit de l'UTF-8 invalide (encodage détecté : %s)", enc)
		}
		if enc != "utf-8" && enc != "windows-1252" {
			t.Fatalf("encodage rapporté inattendu : %q", enc)
		}
	})
}

// FuzzScanner vise le découpage en champs : il ne doit ni paniquer, ni
// boucler indéfiniment, ni produire de champ hors des bornes annoncées.
func FuzzScanner(f *testing.F) {
	for _, seed := range seedLines {
		f.Add(seed, 8)
	}

	f.Fuzz(func(t *testing.T, input string, fields int) {
		if len(input) > 1<<20 || fields < 0 || fields > 100 {
			return
		}
		q := NewQuarantine()
		sc := NewScanner("fuzz", strings.NewReader(input), fields, q.Add)
		lines := 0
		for sc.Scan() {
			lines++
			if lines > 1_000_000 {
				t.Fatal("le Scanner ne termine pas")
			}
			if fields > 0 && len(sc.Fields()) != fields {
				t.Fatalf("Scan a livré %d champs alors que %d sont exigés",
					len(sc.Fields()), fields)
			}
		}
		_ = sc.Err()
	})
}
