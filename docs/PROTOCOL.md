# Protocole WebSocket v1

Point d'entrée : `GET /ws` (WebSocket, texte JSON). Santé : `GET /healthz`.

## Enveloppe

Tous les messages, dans les deux sens :

```json
{ "v": 1, "type": "sim.start", "id": "c12", "data": { … } }
```

| champ | règle |
|---|---|
| `v` | entier, doit valoir `1` (sinon `bad_version`) |
| `type` | 1–32 caractères, voir tables ci-dessous (sinon `unknown_type`) |
| `id` | optionnel, ≤ 32 caractères ASCII imprimables ; recopié dans les réponses |
| `data` | objet propre au type ; absent ≡ `{}` |

Décodage **strict** : champ inconnu, donnée après la valeur JSON, nombre entre
guillemets (`"10"`), flottant pour un entier (`12.5`), `1e999`, UTF-8 invalide,
message > 64 Kio → refus. Un message trop gros ferme la connexion
(`1009 message too big`) ; tout le reste renvoie une erreur et **la connexion
reste ouverte**.

## Client → serveur

| type | data | effet |
|---|---|---|
| `hello` | `{session?: string ≤ 64, client?: string}` | ouvre ou **reprend** une session → `welcome` (+ `race.sync` si une course en direct existe) |
| `ping` | `{}` | → `pong` |
| `scenario.get` | `{seed, cars?}` | → `scenario` |
| `sim.start` | `{seed, cars?, strategies[1..4], sims[1..50000], pace?: "live"\|"fast"}` | → `sim.started`, `sim.progress`…, `sim.done`. Remplace la recherche en cours de la session |
| `sim.cancel` | `{}` | annule → `sim.cancelled` puis `sim.done{truncated:true}` |
| `race.start` | `{seed, cars?, strategy, speed?[1..240]}` | → `race.started`, puis `race.lap`… `race.state` |
| `race.control` | `{action: "pause"\|"resume"\|"stop"}` ou `{action:"speed", speed}` | → `race.state` |

Champs communs :

- `seed` : 1–32 caractères `[A-Za-z0-9_-]`. Une seed purement numérique est lue
  comme un entier, toute autre est hachée (FNV-1a). Même seed ⇒ même circuit,
  mêmes pilotes, même grille, mêmes résultats pour tout le monde.
- `cars` : entier [1, 20], défaut 10.
- `strategy` : `{name ≤ 24, start: "S"|"M"|"H", stops: [{lap, compound}] ≤ 5}`,
  arrêts strictement croissants dans [1, tours−1], au moins deux gommes différentes.

## Serveur → client

| type | data |
|---|---|
| `welcome` | `{session, protocol, resumed, limits}` |
| `scenario` | `{seed, cars, laps, pointsTop, track, drivers[], teams[], grid[], player, suggested, tyres[], limits}` — `tyres[].curve[âge]` = Δ temps au tour (s) |
| `sim.started` | `{run, sims, strategies}` |
| `sim.progress` / `sim.done` | `{run, done, total, races, strategies: Stats[], final, truncated, pointsTop, elapsedMs, reason?}` |
| `race.started` | `{seed, cars, laps, speed, strategy}` |
| `race.lap` | `{lap, laps, flag: "green"\|"chequered", cars: CarLap[], events: Event[]}` |
| `race.state` | `{status: "running"\|"paused"\|"finished"\|"stopped", speed, lap, laps, playhead, autoPaused}` |
| `race.sync` | `{seed, cars, strategy, state, laps: LapView[]}` — rejouée après reconnexion |
| `pong` | `{t}` |
| `error` | `{code, message, field?}` |

`Stats` : `{name, plan, n, hist[cars+1], win, podium, points, dnf, meanPos: Interval,
medPos, p95Pos, cvarPos, worstPos, bestPos, timeMed: Interval, timeP5, timeP95, ahead[]}`
avec `Interval = {p, lo, hi}` (IC 95 %). `hist[cars]` compte les abandons.
`ahead[j]` = fraction des univers où cette stratégie finit devant la stratégie *j*.

Les tableaux ne sont **jamais** `null` (testé sur 300 seeds).

## Codes d'erreur

| code | quand |
|---|---|
| `bad_json` | JSON invalide, enveloppe incorrecte, message binaire |
| `bad_version` | `v` ≠ 1 |
| `unknown_type` | type inconnu |
| `invalid` | donnée hors bornes ; `field` désigne le champ (`strategies[1].stops[0].lap`) |
| `rate_limited` | plus de 15 messages/s soutenus (rafale 40) ; > 200 excès ⇒ fermeture `1008` |
| `busy` | toutes les places de calcul du serveur sont prises |
| `no_race` | `race.control` sans course |
| `internal` | erreur interne (jamais de panic : récupérée et journalisée) |

## Limites

| limite | valeur |
|---|---|
| taille d'un message | 64 Kio |
| simulations par stratégie | 50 000 |
| stratégies par requête | 4 |
| recherches actives par session | 1 (une nouvelle remplace l'ancienne) |
| calculs simultanés (serveur) | 4 |
| budget de temps par calcul | 25 s (au-delà : résultat partiel `truncated`) |
| connexions simultanées | 256 |
| session détachée conservée | 2 min |

Quand le client se déconnecte, sa recherche Monte Carlo est **annulée** et sa
place de calcul libérée (testé) ; une course en direct est mise en pause et
reprend à la reconnexion avec la même `session`.

## Rythme du direct

Le serveur tient une tête de lecture virtuelle (secondes de course) qui avance
de `speed` secondes de course par seconde réelle, et envoie le tour *j* dès que
la tête de lecture atteint la fin du tour *j−2* du leader : le client a toujours
un tour d'avance et son animation ne manque jamais de données.
