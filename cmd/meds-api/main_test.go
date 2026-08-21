package main

import "testing"

// TestHealthcheckURL verrouille la dérivation de l'URL de sonde à partir de
// MEDS_ADDR. Le cas "0.0.0.0:8080" est une non-régression : la première
// version concaténait l'adresse brute et produisait
// "http://127.0.0.10.0.0.0:8080/healthz", soit un conteneur marqué unhealthy
// alors que le service allait bien.
func TestHealthcheckURL(t *testing.T) {
	t.Parallel()

	cas := map[string]struct {
		addr     string
		veut     string
		enErreur bool
	}{
		"vide → défaut":       {addr: "", veut: "http://127.0.0.1:8080/healthz"},
		"port seul":           {addr: ":8080", veut: "http://127.0.0.1:8080/healthz"},
		"joker IPv4":          {addr: "0.0.0.0:9000", veut: "http://127.0.0.1:9000/healthz"},
		"joker IPv6":          {addr: "[::]:8080", veut: "http://127.0.0.1:8080/healthz"},
		"boucle locale":       {addr: "127.0.0.1:8080", veut: "http://127.0.0.1:8080/healthz"},
		"interface nommée":    {addr: "10.0.0.5:8080", veut: "http://10.0.0.5:8080/healthz"},
		"IPv6 nommée":         {addr: "[fd00::1]:8080", veut: "http://[fd00::1]:8080/healthz"},
		"sans port":           {addr: "0.0.0.0", enErreur: true},
		"deux-points en trop": {addr: "1:2:3", enErreur: true},
	}

	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			t.Parallel()

			got, err := healthcheckURL(c.addr)
			if c.enErreur {
				if err == nil {
					t.Fatalf("healthcheckURL(%q) = %q, veut une erreur", c.addr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("healthcheckURL(%q) : erreur inattendue : %v", c.addr, err)
			}
			if got != c.veut {
				t.Errorf("healthcheckURL(%q) = %q, veut %q", c.addr, got, c.veut)
			}
		})
	}
}
