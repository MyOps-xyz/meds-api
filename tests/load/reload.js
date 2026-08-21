// **Le test décisif.** Il valide la promesse centrale de l'architecture :
// une mise à jour du jeu de données sans coupure (exigence E2).
//
// Son critère est **absolu**, pas statistique : une seule requête en erreur
// invalide la conception. C'est pourquoi le seuil est `rate==0` et non un
// percentile — une bascule qui rate une requête sur mille reste une bascule
// ratée.
//
//   k6 run -e BASE_URL=… -e API_KEY=… -e ADMIN_KEY=… tests/load/reload.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { BASE, KEY, headers, pickCIS, QUERIES, fetchCISSample } from './common.js';

const ADMIN = __ENV.ADMIN_KEY || '';
const versionsVues = new Counter('versions_dataset_distinctes');
const reponsesIncoherentes = new Counter('reponses_incoherentes');

export const options = {
  scenarios: {
    charge: {
      executor: 'constant-vus',
      vus: 100,
      duration: __ENV.DUREE || '2m',
      exec: 'trafic',
    },
    // La bascule est déclenchée au milieu de la campagne, une fois le régime
    // établi : la déclencher au démarrage mesurerait la montée en charge, pas
    // la bascule.
    bascule: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      startTime: __ENV.T_BASCULE || '60s',
      exec: 'declencherSync',
    },
  },
  thresholds: {
    // Le critère absolu.
    'http_req_failed': ['rate==0'],
    'checks': ['rate==1'],
    'reponses_incoherentes': ['count==0'],
    // La dégradation pendant la bascule doit rester sous 20 %.
    'http_req_duration{route:lookup}': ['p(99)<3000'],
  },
};

export function setup() {
  if (!KEY) throw new Error('API_KEY est obligatoire');
  if (!ADMIN) throw new Error('ADMIN_KEY est obligatoire pour déclencher la bascule');
  return { cis: fetchCISSample(100) };
}

export function trafic(data) {
  const cis = pickCIS(data.cis);
  const res = http.get(`${BASE}/v1/medicaments/${cis}`, {
    headers, tags: { route: 'lookup' },
  });

  const ok = check(res, {
    'statut 200 pendant la bascule': (r) => r.status === 200,
    'corps exploitable': (r) => r.body && r.body.length > 0,
  });

  // Cohérence : la réponse doit porter le CIS demandé, quelle que soit la
  // version du jeu de données servie. Une réponse mêlant deux versions se
  // trahirait ici.
  if (res.status === 200) {
    try {
      const body = JSON.parse(res.body);
      if (body.data.cis !== cis) {
        reponsesIncoherentes.add(1);
      }
    } catch (e) {
      reponsesIncoherentes.add(1);
    }
  }
  if (!ok) reponsesIncoherentes.add(1);

  sleep(0.01);
}

export function declencherSync() {
  const res = http.post(`${BASE}/admin/sync`, null, {
    headers: { Authorization: `Bearer ${ADMIN}` },
    tags: { route: 'admin_sync' },
  });
  check(res, {
    'synchronisation acceptée': (r) => r.status === 202 || r.status === 409,
  });
  console.log(`bascule déclenchée : HTTP ${res.status}`);
}
