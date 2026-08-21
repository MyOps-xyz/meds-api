// Montée brutale à 1 000 VU : le service doit **dégrader proprement**.
//
// Le critère n'est pas « aucune erreur » — c'est justement le rôle de la
// limitation de débit d'en produire. Le critère est qu'il ne renvoie que des
// `429`, jamais de `5xx` ni de connexion coupée : refuser explicitement vaut
// mieux que s'effondrer, et un client bien élevé sait relire `Retry-After`.
//
//   k6 run -e BASE_URL=… -e API_KEY=… tests/load/spike.js

import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';
import { BASE, KEY, headers, pickCIS, fetchCISSample } from './common.js';

const effondrement = new Rate('effondrement');

export const options = {
  scenarios: {
    pic: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 1000,
      stages: [
        { duration: '20s', target: 100 },
        { duration: '10s', target: 5000 }, // montée brutale
        { duration: '30s', target: 5000 },
        { duration: '20s', target: 100 },  // retour au calme
      ],
    },
  },
  thresholds: {
    // Aucune erreur serveur ni coupure : seuls les 429 sont admis.
    'effondrement': ['rate==0'],
    // Le service doit se rétablir : le p99 global reste borné.
    'http_req_duration': ['p(99)<5000'],
  },
};

export function setup() {
  if (!KEY) throw new Error('API_KEY est obligatoire');
  return { cis: fetchCISSample(100) };
}

export default function (data) {
  const res = http.get(`${BASE}/v1/medicaments/${pickCIS(data.cis)}`, { headers });

  const acceptable = res.status === 200 || res.status === 429;
  effondrement.add(!acceptable);

  check(res, {
    'dégradation propre (200 ou 429)': () => acceptable,
    'un 429 porte Retry-After': (r) => r.status !== 429 || r.headers['Retry-After'],
  });
}
