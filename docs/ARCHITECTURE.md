# Architecture

```
            ┌──────────────────────────── web (TypeScript strict, Vite, Canvas) ───────────────────────────┐
            │ net/protocol.ts  décodeurs à l'exécution ─ net/client.ts  reconnexion + reprise de session    │
            │ track/  piste (couche statique en cache + voitures)   charts/  convergence · falaise          │
            │ live/playback.ts  flux de tours → positions continues    ui/  chrono · stratégies · radio      │
            └───────────────────────────────────────────────▲──────────────────────────────────────────────┘
                                                            │ WebSocket JSON v1 (docs/PROTOCOL.md)
┌───────────────────────────────────────────── server (Go) ─┴───────────────────────────────────────────────┐
│ api/       décodage strict · sessions (reprise) · limiteur · sémaphore de calcul · recover partout         │
│ montecarlo/ N simulations parallèles par lots · points de contrôle déterministes · agrégats en flux         │
│ race/      Scenario (fixe par seed) · State (type valeur = instantané) · Step (0 allocation)                │
│ model/     pneus+falaise · carburant · air sale · dépassement · arrêts       stats/ Wilson · quantiles · CVaR │
│ trackgen/  polygone → arcs → profil de vitesse → temps de réf., zones, usure, stands                          │
│ rng/       SplitMix64, sous-flux Derive(seed, sim, voiture…)                                                  │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

## Déterminisme, par construction

- Le **seul** générateur est `rng.Stream`, un type valeur. Copier un `State`
  copie les flux : un instantané rejoué reproduit exactement la suite (testé),
  base des univers parallèles (palier 3).
- La simulation *i* ne dépend que de `(seed, i)` ; chaque voiture a son
  sous-flux, donc changer la stratégie du joueur ne décale pas les tirages des
  rivaux (comparaisons appariées).
- Les travailleurs écrivent dans des cases disjointes ; l'agrégateur n'avance
  que sur le **préfixe contigu** de lots terminés, dans l'ordre des indices, et
  publie aux points de contrôle fixes. Résultat et flux de progression sont
  identiques octet pour octet avec 1 ou 16 goroutines (`pitwall verify`, CI).

## Performance

`race.State` est un bloc de tableaux de taille fixe (structure de tableaux) ;
`Step` n'alloue rien, évite `math.Min/Max`, la logistique et la loi normale sont
tabulées. Mesure de référence (`make bench`, 1 cœur Xeon 2,1 GHz) : ≈ 20–22 k
courses/s (20 voitures × 50 tours), ≈ 80–90 k/s sur 4 cœurs.

## Robustesse

| menace | parade | preuve |
|---|---|---|
| JSON géant / malformé | limite 64 Kio, décodage strict | `TestGiantMessageClosesConnection`, `FuzzDecode` |
| valeurs absurdes | bornes explicites, messages en français avec `field` | `TestInvalidInputsNeverKillTheConnection` (36 cas) |
| panic | `recover` dans chaque goroutine et le handler HTTP | fuzz + tests de propriétés |
| saturation | sémaphore global, 1 recherche/session, débit limité, budget temps | `TestBusyWhenJobsExhausted`, `TestRateLimit` |
| client qui part | annulation par contexte, libération de la place | `TestClientLeavingCancelsJob` |
| reconnexion | session conservée 2 min, `race.sync` | `TestLiveRaceAndResume`, `client.test.ts` |
| message serveur inattendu | décodeurs → `null`, jamais d'exception | `protocol.test.ts` (fuzz 5 000 mutations) |
| injection HTML | texte serveur inséré en nœuds texte | `ui/dom.ts#h` |
