# Plan

## Paliers

| palier | contenu | état |
|---|---|---|
| **1 · MVP** | circuit généré, 10 voitures, 3 gommes, usure + falaise, arrêts, Monte Carlo sur stratégies comparées, histogramme convergent, piste animée, tour de chronométrage | ✅ livré |
| **2 · Solide** | 20 voitures par défaut, voiture de sécurité (réelle/virtuelle), drapeau rouge, météo évolutive + gommes pluie, crevaisons, optimiseur (faisceau + successive halving), Live Race avec recalcul à chaque imprévu, fan chart | à venir |
| **3 · Wahou** | univers parallèles (branchement depuis un instantané), analyse du regret, partage par seed (fait dès le palier 1), circuits variés (fait), étalonnage CSV de la courbe d'usure, sons, finitions | à venir |

Règle : on ne passe au palier suivant que si `make verify` et `make e2e` sont verts, et après une passe redteam.

## Structure

```
server/   moteur + serveur Go (voir docs/ARCHITECTURE.md)
web/      frontend TypeScript (Vite, Canvas)
docs/     MODELS.md (formules, unités, bornes) · PROTOCOL.md · ARCHITECTURE.md · screenshots/
.opencode/ agents (engine, frontend, redteam, reviewer) · commandes · plugin quality-gate · skill
.github/  CI : lint, types, tests, fuzz, déterminisme, bench, e2e, image Docker
```

## Hypothèses principales des modèles (détail : MODELS.md)

1. Temps au tour additif : base + pilote/voiture + forme du jour + pneus + carburant + air sale + bruit + fautes.
2. Pneus : usure linéaire en tours, perte linéaire puis **falaise quadratique** au-delà d'un seuil d'usure.
3. Carburant : perte linéaire en masse, consommation constante par tour.
4. Dépassement : probabilité logistique de l'avantage de rythme, ajustée par la facilité du circuit et la défense.
5. Arrêts : perte de voie des stands propre au circuit + immobilisation demi-normale + arrêts lents rares.
6. Pannes : taux de panne constant par tour (processus de Bernoulli).
7. Rivaux : plans raisonnables, connus à ±2 tours près.
8. Circuits : profil de vitesse limité par l'adhérence latérale, l'accélération et le freinage ; tout le reste (temps de référence, usure, dépassement, stands) en découle.
