// Charge nominale représentative : 200 VU pendant 5 minutes.
//
// La répartition des requêtes imite un intégrateur réel — beaucoup de
// lookups par code, quelques recherches, peu de fiches complètes. Une charge
// composée uniquement de lookups donnerait un débit flatteur et sans rapport
// avec l'usage.
//
//   k6 run -e BASE_URL=… -e API_KEY=… tests/load/mixed.js

import http from 'k6/http';
import { sleep } from 'k6';
import { BASE, KEY, headers, pickCIS, QUERIES, fetchCISSample, checkOK } from './common.js';

export const options = {
  scenarios: {
    nominal: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 200 },
        { duration: '4m', target: 200 },
        { duration: '30s', target: 0 },
      ],
    },
  },
  // Ces seuils sont **de bout en bout**, réseau compris. Ils ne sont pas ceux
  // de docs/07-performance.md §2.1, qui sont explicitement « hors réseau ».
  //
  // La distinction n'est pas un détail : mesuré le 15/08/2026, k6 exécuté en
  // conteneur sur macOS rapporte un p99 de ~207 ms **identique sur toutes les
  // routes**, y compris les plus légères. Une latence uniforme sur des routes
  // dont le coût serveur varie d'un facteur vingt-cinq ne mesure pas le
  // serveur : c'est la pile réseau de la machine virtuelle. Y adosser un
  // budget de 200 µs reviendrait à faire échouer la campagne sur une
  // caractéristique du poste de test.
  //
  // Les budgets à la microseconde se vérifient **côté serveur**, sur
  // l'histogramme Prometheus — voir `make load-check`, exécuté après ce
  // scénario. C'est la seule mesure qui isole le temps de traitement.
  thresholds: {
    'http_req_failed': ['rate==0'],
    'checks': ['rate==1'],
    // Bornes de bout en bout : elles détectent un effondrement, pas une
    // régression de quelques dizaines de microsecondes.
    'http_req_duration{route:lookup_cis}': ['p(95)<500'],
    'http_req_duration{route:recherche}': ['p(95)<1000'],
    'http_req_duration{route:suggest}': ['p(95)<500'],
    'http_req_duration{route:fiche_complete}': ['p(95)<1000'],
  },
};

export function setup() {
  if (!KEY) throw new Error('API_KEY est obligatoire');
  return { cis: fetchCISSample(100) };
}

export default function (data) {
  // 60 % lookups, 25 % recherches, 10 % suggest, 5 % fiche complète.
  const r = Math.random();
  if (r < 0.6) {
    const res = http.get(`${BASE}/v1/medicaments/${pickCIS(data.cis)}`, {
      headers, tags: { route: 'lookup_cis' },
    });
    checkOK(res, 'lookup CIS');
  } else if (r < 0.85) {
    const q = QUERIES[Math.floor(Math.random() * QUERIES.length)];
    const res = http.get(`${BASE}/v1/medicaments?q=${q}&limit=20`, {
      headers, tags: { route: 'recherche' },
    });
    checkOK(res, 'recherche');
  } else if (r < 0.95) {
    const q = QUERIES[Math.floor(Math.random() * QUERIES.length)].slice(0, 4);
    const res = http.get(`${BASE}/v1/suggest?q=${q}&limit=10`, {
      headers, tags: { route: 'suggest' },
    });
    checkOK(res, 'suggest');
  } else {
    const res = http.get(`${BASE}/v1/medicaments/${pickCIS(data.cis)}?include=all`, {
      headers, tags: { route: 'fiche_complete' },
    });
    checkOK(res, 'fiche complète');
  }
  sleep(0.05);
}
