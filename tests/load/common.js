// Fonctions partagées par les quatre scénarios de charge (T-55).
//
// Les budgets repris ici sont ceux de docs/07-performance.md §2. Ils sont
// exprimés en seuils k6 (`thresholds`) et non en simples assertions : un
// seuil franchi fait échouer le scénario avec un code de sortie non nul,
// donc casse l'intégration continue. Une mesure qu'on peut ignorer ne sert
// à rien.

import http from 'k6/http';
import { check } from 'k6';

export const BASE = __ENV.BASE_URL || 'http://127.0.0.1:18080';
export const KEY = __ENV.API_KEY || '';

export const headers = {
  Authorization: `Bearer ${KEY}`,
  Accept: 'application/json',
};

// Les codes CIS sont tirés d'un échantillon réel plutôt que générés : un CIS
// inexistant emprunte le chemin du 404, qui ne mesure ni la sérialisation ni
// les index. On mesurerait alors la mauvaise chose.
export function pickCIS(cisList) {
  return cisList[Math.floor(Math.random() * cisList.length)];
}

export const QUERIES = [
  'doliprane', 'paracetamol', 'ibuprofene', 'amlodipine', 'sanofi',
  'comprime', 'aspirine', 'metformine', 'omeprazole', 'levothyrox',
];

// Requêtes tolérant les fautes : elles empruntent le repli trigramme, dont le
// budget est distinct (3 ms, chemin dégradé).
export const FUZZY_QUERIES = ['paracetamo', 'paracetamoll', 'dolipranne', 'ibuprofenne'];

export function fetchCISSample(n) {
  const res = http.get(`${BASE}/v1/medicaments?limit=100`, { headers });
  if (res.status !== 200) {
    throw new Error(`échantillonnage impossible : HTTP ${res.status} — ${res.body}`);
  }
  const list = JSON.parse(res.body).data.map((d) => d.cis);
  if (list.length === 0) throw new Error('aucun médicament servi par l’API');
  return list.slice(0, n);
}

export function checkOK(res, name) {
  return check(res, {
    [`${name} : 200`]: (r) => r.status === 200,
    [`${name} : corps non vide`]: (r) => r.body && r.body.length > 0,
  });
}
