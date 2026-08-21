// Endurance : 50 VU pendant une heure, RSS stable.
//
// Ce que ce test cherche n'est pas la performance mais la **fuite** : une
// allocation par requête jamais libérée, une table de seaux de limitation qui
// croît sans purge, une goroutine qui ne se termine pas. Rien de tout cela ne
// se voit sur cinq minutes.
//
// La mémoire n'est pas mesurée par k6 mais relevée sur `/metrics` :
// `process_resident_memory_bytes` et `go_goroutines`. Le scénario ne fait que
// maintenir la charge ; le verdict se lit dans Prometheus ou dans le
// relevé produit par `make load-soak`.
//
//   k6 run -e BASE_URL=… -e API_KEY=… -e DUREE=1h tests/load/soak.js

import http from 'k6/http';
import { sleep } from 'k6';
import { BASE, KEY, headers, pickCIS, QUERIES, fetchCISSample, checkOK } from './common.js';

export const options = {
  scenarios: {
    endurance: {
      executor: 'constant-vus',
      vus: 50,
      duration: __ENV.DUREE || '1h',
    },
  },
  thresholds: {
    'http_req_failed': ['rate==0'],
    'http_req_duration': ['p(99)<3000'],
  },
};

export function setup() {
  if (!KEY) throw new Error('API_KEY est obligatoire');
  return { cis: fetchCISSample(100) };
}

export default function (data) {
  // Une requête de chaque famille par itération : une charge homogène
  // n'exercerait qu'un seul chemin d'allocation.
  checkOK(http.get(`${BASE}/v1/medicaments/${pickCIS(data.cis)}`, { headers }), 'lookup');
  const q = QUERIES[Math.floor(Math.random() * QUERIES.length)];
  checkOK(http.get(`${BASE}/v1/medicaments?q=${q}`, { headers }), 'recherche');
  checkOK(http.get(`${BASE}/v1/dataset`, { headers }), 'dataset');
  sleep(1);
}
