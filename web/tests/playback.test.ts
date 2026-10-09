import { describe, expect, it } from "vitest";
import { Playback } from "../src/live/playback";
import { lap, squareTrack } from "./fixtures";

describe("playback", () => {
  it("moves cars continuously and never extrapolates", () => {
    const pb = new Playback(squareTrack(), 2, [0, 1]);
    expect(
      pb.add(
        lap(1, 3, [
          { car: 0, time: 80 },
          { car: 1, time: 81 },
        ]),
      ),
    ).toBe(true);
    expect(pb.available).toBe(80);
    const a = pb.pose(0, 40);
    expect(a.lap).toBeCloseTo(0.5, 1);
    // no data for lap 2 yet: holds at the line
    const b = pb.pose(0, 100);
    expect(b.lap).toBe(1);
    expect(pb.lapAt(79)).toBeNull();
    expect(pb.lapAt(80)?.lap).toBe(1);
  });

  it("refuses gaps and tolerates duplicates", () => {
    const pb = new Playback(squareTrack(), 1, [0]);
    expect(pb.add(lap(2, 3, [{ car: 0, time: 160 }]))).toBe(false);
    expect(pb.add(lap(1, 3, [{ car: 0, time: 80 }]))).toBe(true);
    expect(pb.add(lap(1, 3, [{ car: 0, time: 80 }]))).toBe(true);
    expect(pb.laps.length).toBe(1);
  });

  it("drives the pit lane on an in-lap and rejoins at the line", () => {
    const pb = new Playback(squareTrack(), 1, [0]);
    pb.add(lap(1, 3, [{ car: 0, time: 102.4, pitted: true }]));
    const early = pb.pose(0, 10);
    expect(early.inPit).toBe(false);
    const late = pb.pose(0, 101);
    expect(late.inPit).toBe(true);
    const end = pb.pose(0, 102.39);
    expect(end.x).toBeCloseTo(0, 1);
    expect(end.y).toBeCloseTo(0, 1);
  });

  it("runs until the last car finishes after the flag", () => {
    const pb = new Playback(squareTrack(), 2, [0, 1]);
    pb.add(
      lap(1, 1, [
        { car: 0, time: 80 },
        { car: 1, time: 95 },
      ]),
    );
    expect(pb.finished).toBe(true);
    expect(pb.available).toBe(95);
  });

  it("parks retired cars", () => {
    const pb = new Playback(squareTrack(), 2, [0, 1]);
    pb.add(
      lap(1, 3, [
        { car: 0, time: 80 },
        { car: 1, time: 0, out: true },
      ]),
    );
    expect(pb.pose(1, 50).out).toBe(true);
  });
});
