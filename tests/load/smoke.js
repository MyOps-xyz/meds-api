// Fumée : une requête par endpoint, pour vérifier qu'un déploiement répond
// avant d'engager une campagne de charge.
//
// C'est aussi le test de recette exécuté après `docker compose up` sur les
// deux profils de frontal (T-56).
//
//   k6 run -e BASE_URL=… -e API_KEY=… tests/load/smoke.js

import http from 'k6/http';
import { check, group } from 'k6';
import { BASE, KEY, headers, fetchCISSample } from './common.js';

// VIA_FRONTAL=1 lorsque la fumée passe par Caddy ou Traefik.
const VIA_FRONTAL = __ENV.VIA_FRONTAL === '1';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    'checks': ['rate==1'],
    'http_req_duration': ['p(95)<1000'],
  },
};

export default function () {
  group('exploitation', () => {
    check(http.get(`${BASE}/healthz`), { 'healthz 200': (r) => r.status === 200 });
    check(http.get(`${BASE}/readyz`), { 'readyz 200': (r) => r.status === 200 });
    check(http.get(`${BASE}/openapi.json`), {
      'openapi 200': (r) => r.status === 200,
      'openapi est du JSON 3.1': (r) => r.status === 200 && JSON.parse(r.body).openapi === '3.1.0',
    });
    check(http.get(`${BASE}/docs`), { 'docs 200': (r) => r.status === 200 });
  });

  group('sécurité', () => {
    check(http.get(`${BASE}/v1/medicaments`), {
      'sans clé : 401': (r) => r.status === 401,
      'le 401 est un problem+json': (r) => (r.headers['Content-Type'] || '').includes('problem+json'),
    });
    // Le refus de /metrics est le fait du **frontal**, pas de l'application :
    // celle-ci le sert normalement, et Prometheus la scrute en direct sur le
    // réseau interne (ADR 0006). L'assertion n'a donc de sens que lorsque la
    // fumée passe par Caddy ou Traefik — d'où le drapeau.
    if (VIA_FRONTAL) {
      check(http.get(`${BASE}/metrics`), {
        'metrics refusé par le frontal': (r) => r.status === 403 || r.status === 404,
      });
    } else {
      check(http.get(`${BASE}/metrics`), {
        'metrics servi par l’application (accès direct)': (r) => r.status === 200,
      });
    }
    const res = http.get(`${BASE}/v1/dataset`, { headers });
    check(res, {
      'en-têtes de sécurité': (r) =>
        r.headers['X-Content-Type-Options'] === 'nosniff' &&
        r.headers['X-Frame-Options'] === 'DENY',
      'aucun en-tête Server': (r) => !r.headers['Server'],
    });
  });

  const cis = fetchCISSample(1);

  group('endpoints métier', () => {
    const routes = [
      `/v1/medicaments?q=doliprane`,
      `/v1/medicaments/${cis[0]}`,
      `/v1/medicaments/${cis[0]}?include=all`,
      `/v1/medicaments/${cis[0]}/presentations`,
      `/v1/medicaments/${cis[0]}/composition`,
      `/v1/medicaments/${cis[0]}/generiques`,
      `/v1/medicaments/${cis[0]}/avis`,
      `/v1/medicaments/${cis[0]}/conditions`,
      `/v1/medicaments/${cis[0]}/ruptures`,
      `/v1/substances`,
      `/v1/groupes-generiques`,
      `/v1/ruptures`,
      `/v1/mitm`,
      `/v1/suggest?q=dol`,
      `/v1/dataset`,
    ];
    for (const route of routes) {
      check(http.get(`${BASE}${route}`, { headers }), {
        [`${route} : 200`]: (r) => r.status === 200,
      });
    }
  });

  group('cache', () => {
    const first = http.get(`${BASE}/v1/medicaments?q=doliprane`, { headers });
    const etag = first.headers['Etag'] || first.headers['ETag'];
    check(first, { 'ETag présent': () => !!etag });
    if (etag) {
      const second = http.get(`${BASE}/v1/medicaments?q=doliprane`, {
        headers: { ...headers, 'If-None-Match': etag },
      });
      check(second, { 'If-None-Match → 304': (r) => r.status === 304 });
    }
  });
}
