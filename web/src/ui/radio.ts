// Deterministic race-engineer radio: fixed templates, the variant is picked
// from the event itself (lap, car), so the same race always sounds the same.
import type { Driver, LapView, RaceEvent } from "../net/protocol";

export interface RadioMsg {
  lap: number;
  text: string;
  alert: boolean;
}

const COMPOUND = { S: "tendres", M: "mediums", H: "dures" } as const;

function pick<T>(xs: readonly T[], k: number): T {
  return xs[Math.abs(k) % xs.length] as T;
}

export function radioFor(ev: RaceEvent, lap: LapView, drivers: Driver[], player: number): RadioMsg | null {
  const code = (c: number) => drivers[c]?.code ?? "???";
  const pos = lap.cars.find((c) => c.car === player)?.pos ?? 0;
  const k = ev.lap * 31 + ev.car * 7;
  const isP = ev.car === player;
  switch (ev.kind) {
    case "pit":
      if (!isP) return null;
      return {
        lap: ev.lap,
        alert: true,
        text: pick(
          [
            `Box, box. On passe en ${COMPOUND[ev.compound]}. ${ev.value.toFixed(1)} s à l'arrêt.`,
            `Arrêt effectué, ${ev.value.toFixed(1)} s. ${COMPOUND[ev.compound]} montées, pousse sur le tour de sortie.`,
            `Dans les stands ce tour. Gommes ${COMPOUND[ev.compound]}, chauffe-les bien.`,
          ],
          k,
        ),
      };
    case "slowstop":
      return isP
        ? {
            lap: ev.lap,
            alert: true,
            text: `Arrêt lent, on perd ${ev.value.toFixed(1)} s. Désolé, on reprend.`,
          }
        : null;
    case "pass":
      if (isP)
        return {
          lap: ev.lap,
          alert: false,
          text: pick(
            [
              `Bien joué, ${code(ev.other)} est derrière. P${pos}.`,
              `Dépassement sur ${code(ev.other)}. Garde la porte fermée.`,
            ],
            k,
          ),
        };
      if (ev.other === player)
        return {
          lap: ev.lap,
          alert: true,
          text: pick(
            [
              `${code(ev.car)} est passé. On reste calme, P${pos}.`,
              `Perdu une place sur ${code(ev.car)}. Il a des pneus plus frais.`,
            ],
            k,
          ),
        };
      return null;
    case "mistake":
      return isP
        ? {
            lap: ev.lap,
            alert: true,
            text: `Petite erreur, ${ev.value.toFixed(1)} s perdues. Concentration.`,
          }
        : null;
    case "retire":
      if (isP)
        return { lap: ev.lap, alert: true, text: "On s'arrête. Je répète, on s'arrête. Rentre la voiture." };
      return { lap: ev.lap, alert: false, text: `${code(ev.car)} abandonne, problème mécanique.` };
    case "fastest":
      return isP && ev.lap > 1
        ? { lap: ev.lap, alert: false, text: `Meilleur tour en course : ${ev.value.toFixed(3)}. Superbe.` }
        : null;
  }
}

export function radioForLap(lap: LapView, drivers: Driver[], player: number): RadioMsg[] {
  const out: RadioMsg[] = [];
  for (const ev of lap.events) {
    const m = radioFor(ev, lap, drivers, player);
    if (m) out.push(m);
  }
  const pos = lap.cars.find((c) => c.car === player)?.pos ?? 0;
  if (lap.lap === lap.laps - 1)
    out.push({ lap: lap.lap, alert: true, text: `Dernier tour. P${pos}, ramène-la.` });
  if (lap.flag === "chequered")
    out.push({
      lap: lap.lap,
      alert: true,
      text:
        pos === 1
          ? "Drapeau à damier ! VICTOIRE ! Quelle course !"
          : pos <= 3
            ? `Drapeau à damier, P${pos}. Podium, bravo.`
            : `Drapeau à damier, P${pos}.`,
    });
  return out;
}
