# Modèles et hypothèses

Tout ce qui suit est implémenté dans `server/internal/model` (formules de course),
`server/internal/trackgen` (circuits) et `server/internal/race` (moteur). Chaque
paramètre a une **unité** et des **bornes** ; `TestParametersHaveUnitsAndBounds`
vérifie automatiquement que chaque constante de `model.go` déclare
`unité, sens [min, max]`, respecte ses bornes et figure dans ce document.

Unités : temps en **s**, masse en **kg**, distance en **m**, vitesse en **m/s**
(km/h à l'affichage), usure = **fraction sans dimension** de la vie du pneu.

## 1. Temps au tour

Pour la voiture *c* au tour *k* (tour 1 = départ arrêté) :

```
t(c,k) = base                                   circuit (§5)
       + pace_c + form_c                         voiture+pilote, forme du jour
       + Δpneu(gomme, w)                         §2
       + carburant_c(k) · FuelSecPerKg           §3
       + warmup(gomme)        si le tour suit un arrêt
       + air_sale(intervalle) si la voiture suit à < DirtyAirGapS   §4
       + σ_c · N(0,1)                            variabilité tour à tour
       + faute                                   avec proba MistakeP_c
       + [tour 1] StartPenaltyS + GridSlotS·place + StartSigmaS·|N(0,1)|
```

| symbole | unité | bornes | source |
|---|---|---|---|
| `base` | s | [60, 125] | tour de référence du circuit : medium neuf, sans carburant, air propre |
| `pace_c` | s/tour | [-1.2, 1.8] | écurie (étalée sur ±0,5 s) + pilote (±0,15 s) |
| `form_c` = `DayFormSigmaS`·N(0,1) | s/tour | σ ∈ [0, 0.4] | tirée une fois par course : réglages, adaptation au circuit |
| `σ_c` (`SigmaS`) | s | [0.08, 0.6] | constance du pilote |
| `MistakeP_c` | 1/tour | [0, 0.03] | probabilité d'erreur |
| faute | s | min(Exp(`MistakeMeanS`), `MistakeMaxS`) | `MistakeMeanS` ∈ [0, 5], `MistakeMaxS` ∈ [0, 20] |
| `StartPenaltyS` | s | [0, 10] | départ arrêté |
| `GridSlotS` | s | [0, 0.5] | écart par place sur la grille |
| `StartSigmaS` | s | [0, 1.5] | qualité de l'envol (demi-normale : on ne gagne pas de temps au départ) |

**Hypothèse — bruit normal tronqué.** N(0,1) est tiré par table inverse de la
fonction de répartition (4096 cellules, interpolation linéaire), tronquée à
±3,7σ. Les queues lourdes réelles (erreurs, incidents) sont modélisées
explicitement par les fautes et les arrêts lents, pas par le bruit.

## 2. Pneus et falaise

Usure (sans dimension) après `âge` tours sur un circuit d'usure `W` pour un
pilote de facteur `TyreSave` :

```
w = âge · WearPerLap(gomme) · W · TyreSave
Δpneu(w) = PaceS + DegS · w + CliffS · max(0, (w − Cliff) / 0.1)²
```

Δpneu est continu, croissant et **convexe au-delà de la falaise** (`Cliff`) :
le temps au tour s'effondre. Tour de falaise : `Cliff / (WearPerLap · W · TyreSave)`.

| gomme | PaceS (s) | WearPerLap (1/tour) | DegS (s) | Cliff | CliffS (s) | WarmupS (s) | falaise si W=1 |
|---|---|---|---|---|---|---|---|
| S tendre | −0.70 | 0.046 | 1.6 | 0.70 | 0.65 | 0.35 | ≈ 15 tours |
| M medium | 0.00 | 0.030 | 1.3 | 0.70 | 0.65 | 0.65 | ≈ 23 tours |
| H dure | +0.55 | 0.020 | 1.1 | 0.72 | 0.65 | 1.05 | ≈ 36 tours |

Bornes (`TyreSpec`, testées par `TestTyreTableBounds`) : PaceS ∈ [−1.5, 1.5] s,
WearPerLap ∈ [0.005, 0.1], DegS ∈ [0, 4] s, Cliff ∈ [0.4, 1], CliffS ∈ [0, 3] s,
WarmupS ∈ [0, 2] s. `TyreSave` ∈ [0.85, 1.15].

**Hypothèses.** Gommes équilibrées pour qu'un 1-arrêt soit possible mais qu'un
2-arrêts soit compétitif. Pas de thermique fine : le seul effet de température
est le tour de chauffe après un arrêt. Règlement : au moins **deux gommes
sèches différentes** par course (validé côté serveur).

## 3. Carburant

```
carburant_c(0) = tours · FuelPerLapKg + FuelMarginKg
carburant_c(k+1) = max(0, carburant_c(k) − FuelPerLapKg)
Δcarburant = carburant · FuelSecPerKg
```

| paramètre | unité | bornes |
|---|---|---|
| `FuelPerLapKg` = 0,31 kg/km × longueur | kg/tour | [1.2, 2.0] |
| `FuelMarginKg` | kg | [0, 5] |
| `FuelSecPerKg` | s/kg | [0.02, 0.045] |

Pas de ravitaillement ; le carburant ne peut pas devenir négatif (invariant testé).

## 4. Trafic, air sale, dépassements

**Air sale.** Si l'intervalle *i* (s) avec la voiture devant au début du tour est
< `DirtyAirGapS` : perte `DirtyAirS · (1 − i / DirtyAirGapS)`.

**Franchissement de la ligne.** Les voitures sont traitées dans l'ordre de la
course. Si une voiture arrive à moins de `MinGapS` de celle de devant, elle
tente un dépassement avec la probabilité

```
p = 0.92 · logistique((avantage − seuil) / 0.28),  seuil = 0.55 / (facilité · 1/Defending_défenseur)
```

où `avantage` est la différence de temps au tour *potentiel* (pneus, carburant,
rythme). Succès : l'attaquant passe, les deux perdent `PassFightS`. Échec :
l'attaquant est bloqué à `MinGapS` derrière. Au plus `MaxPassesLap` dépassements
par voiture et par tour. La logistique est tabulée (2049 points, erreur < 2·10⁻⁶).

| paramètre | unité | bornes |
|---|---|---|
| `DirtyAirGapS` | s | [0.3, 3] |
| `DirtyAirS` | s/tour | [0, 1.5] |
| `MinGapS` | s | [0.05, 1] |
| `PassFightS` | s | [0, 1] |
| `MaxPassesLap` | nombre | [1, 5] |
| facilité (`OvertakeEase`) | sans dim. | [0.4, 1.6] |
| `Defending` | sans dim. | [0.8, 1.25] |

**Hypothèse — drapeaux bleus.** Les voitures sur des tours différents
n'interagissent pas (le retardataire s'efface).

## 5. Circuits générés (`trackgen`)

1. Polygone étoilé de 9–14 sommets (+ « crochets » aléatoires pour chicanes et
   enchaînements), sommets arrondis par des **arcs de rayon explicite**
   (épingle ≈ 15 m, courbe rapide ≈ 250 m), puis rééchantillonnage à abscisse
   curviligne uniforme (480 points). Rejet des tracés qui se croisent ou se frôlent.
2. Mise à l'échelle à la longueur cible ∈ [3 900, 6 300] m.
3. **Profil de vitesse** : `v ≤ min(vMax, √(aLat/|κ|))` puis passes avant
   (accélération limitée) et arrière (freinage limité).
   `vMax` = 91 m/s, `aLat` = 31 m/s², `aBrake` = 38 m/s², `aAccel` = 8 m/s², `vMin` = 19 m/s.
4. **Tour de référence** = ∫ ds / v ∈ [60, 125] s. Tours de course = 255 km / longueur, bornés à [38, 66].
5. **Usure** `WearFactor` ∈ [0.75, 1.35] : charge latérale moyenne ∫ v²|κ| ds / L normalisée, ×(0,92–1,08) d'abrasivité.
6. **Zones de dépassement** (≤ 3) : lignes droites ≥ 300 m terminées par un freinage de plus de 20 m/s. `OvertakeEase` = 0,4 + Σ longueurs / 2000 m, borné [0.4, 1.6].
7. **Stands** : voie de 380 m le long de la fin de la ligne droite, sortie sur la ligne. `PitLossS` = longueur / 22,2 m/s − temps en piste + 5,5 s (décélération / réaccélération) + 0–4 s, borné [16, 27] s.

## 6. Arrêts aux stands, pannes

```
perte d'un arrêt = PitLossS (circuit) + PitStationaryS + PitSigmaS·|N(0,1)| [+ arrêt lent]
arrêt lent : proba SlowStopP, durée min(Exp(SlowStopMeanS), SlowStopMaxS)
abandon mécanique : proba DNFPerLap par voiture et par tour
```

| paramètre | unité | bornes |
|---|---|---|
| `PitStationaryS` | s | [1.5, 6] |
| `PitSigmaS` | s | [0, 1] |
| `SlowStopP` | proba / arrêt | [0, 0.2] |
| `SlowStopMeanS` | s | [0, 10] |
| `SlowStopMaxS` | s | [0, 30] |
| `DNFPerLap` | 1/tour | [0, 0.01] (≈ 2,5 % par course de 50 tours) |

L'arrêt se fait **à la fin** du tour annoncé (tour d'entrée) ; la nouvelle gomme
part du tour suivant (tour de sortie, avec `WarmupS`). Un arrêt n'est possible
qu'entre la fin du tour 1 et l'avant-dernier tour ; au plus 5 arrêts ; au plus un
par tour (invariant testé).

## 7. Rivaux et incertitude

Chaque rival a un plan de base calculé pour rester avant la falaise (nombre
d'arrêts minimal, relais proportionnels à la vie de chaque gomme, 30 % des
rivaux tentent un arrêt de plus). Dans chaque simulation, chaque arrêt rival est
**décalé de ±2 tours** : le joueur ne connaît pas le tour exact des arrêts adverses.

## 8. Monte Carlo

- Simulation *i* : flux `rng(seed).Derive(LabelSim, i)` ; chaque voiture a son
  sous-flux `Derive(LabelCar, c)`. Toutes les stratégies d'une requête sont
  évaluées **sur les mêmes N univers** (nombres aléatoires communs) : la
  comparaison est appariée, « A devant B dans 67 % des univers » a un sens.
- Agrégation sur le **préfixe contigu** de simulations terminées, à des points
  de contrôle déterministes (×1,25) : le flux de progression lui-même est
  identique quel que soit le nombre de goroutines.
- Probabilités : intervalle de **Wilson** à 95 %. Temps médian : IC par
  statistiques d'ordre binomiales. Position moyenne : IC normal.
  Risque : 95e centile de la position et **CVaR 5 %** (moyenne des 5 % pires
  issues). L'IC se resserre en 1/√N (testé).
- « Points » = top `min(10, ⌈N/2⌉)` (top 5 avec 10 voitures, top 10 avec 20).

## 9. Ce qui n'est pas (encore) modélisé

Palier 1 : pas de météo, pas de voiture de sécurité, pas de crevaison (paliers 2-3).
Pas d'évolution de la piste au fil de la course, pas de gestion d'ERS, pas
d'ordres d'équipe. Les ordres de grandeur sont plausibles, pas calibrés sur des
données réelles (un étalonnage par CSV est prévu au palier 3).
