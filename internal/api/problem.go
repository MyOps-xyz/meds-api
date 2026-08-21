// Package api est la couche HTTP : routage, sérialisation, erreurs,
// middlewares de sécurité et handlers.
package api

import (
	"encoding/json"
	"net/http"
)

// TypeBaseURL préfixe les identifiants de type d'erreur. Ce sont des URI de
// documentation, pas des adresses à déréférencer.
const TypeBaseURL = "https://meds-api.example/errors/"

// Types d'erreur du catalogue (docs/04-specification-api.md §1.3). Ce sont
// des identifiants de contrat : un client peut s'y fier pour brancher un
// comportement, là où `detail` est destiné à un humain et peut évoluer.
const (
	ErrInvalidParameter = "invalid_parameter"
	ErrCursorStale      = "cursor_stale"
	ErrUnauthorized     = "unauthorized"
	ErrNotFound         = "not_found"
	ErrNotAcceptable    = "not_acceptable"
	ErrRateLimited      = "rate_limited"
	ErrInternal         = "internal_error"
	ErrNotReady         = "not_ready"
	ErrConflict         = "conflict"
	ErrPayloadTooLarge  = "payload_too_large"
	ErrMethodNotAllowed = "method_not_allowed"
)

// Problem est une réponse d'erreur RFC 9457.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	RequestID string `json:"request_id"`
}

// titles associe un type d'erreur à son titre, court et stable.
var titles = map[string]string{
	ErrInvalidParameter: "Paramètre invalide",
	ErrCursorStale:      "Curseur périmé",
	ErrUnauthorized:     "Authentification requise",
	ErrNotFound:         "Ressource introuvable",
	ErrNotAcceptable:    "Format de réponse non acceptable",
	ErrRateLimited:      "Quota dépassé",
	ErrInternal:         "Erreur interne",
	ErrNotReady:         "Service non prêt",
	ErrConflict:         "Opération déjà en cours",
	ErrPayloadTooLarge:  "Corps de requête trop volumineux",
	ErrMethodNotAllowed: "Méthode non autorisée",
}

// statuses associe un type d'erreur à son statut HTTP.
var statuses = map[string]int{
	ErrInvalidParameter: http.StatusBadRequest,
	ErrCursorStale:      http.StatusBadRequest,
	ErrUnauthorized:     http.StatusUnauthorized,
	ErrNotFound:         http.StatusNotFound,
	ErrNotAcceptable:    http.StatusNotAcceptable,
	ErrRateLimited:      http.StatusTooManyRequests,
	ErrInternal:         http.StatusInternalServerError,
	ErrNotReady:         http.StatusServiceUnavailable,
	ErrConflict:         http.StatusConflict,
	ErrPayloadTooLarge:  http.StatusRequestEntityTooLarge,
	ErrMethodNotAllowed: http.StatusMethodNotAllowed,
}

// WriteProblem écrit une erreur au format RFC 9457.
//
// detail est destiné à un humain et doit rester **exempt de toute donnée
// interne** : ni trace d'exécution, ni chemin de fichier, ni message d'erreur
// Go brut. Un 500 ne dit jamais pourquoi ; il donne un request_id, que
// l'exploitant retrouve dans les logs. C'est ce qui évite qu'une anomalie
// serveur devienne une fuite d'information.
func WriteProblem(w http.ResponseWriter, r *http.Request, errType, detail string) {
	status, ok := statuses[errType]
	if !ok {
		status = http.StatusInternalServerError
		errType = ErrInternal
	}
	p := Problem{
		Type:      TypeBaseURL + errType,
		Title:     titles[errType],
		Status:    status,
		Detail:    detail,
		Instance:  r.URL.Path,
		RequestID: RequestIDFrom(r.Context()),
	}

	// Un corps d'erreur n'est jamais mis en cache : le client doit
	// reposer sa question, pas rejouer l'échec.
	h := w.Header()
	h.Set("Content-Type", "application/problem+json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Del("ETag")

	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(p)
}
