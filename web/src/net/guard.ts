// Tiny runtime decoders. Every message from the server goes through them:
// a malformed or unexpected payload yields `null`, never an exception.

export type Decoder<T> = (v: unknown) => T | null;

const isObj = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);

export const num: Decoder<number> = (v) => (typeof v === "number" && Number.isFinite(v) ? v : null);
export const int: Decoder<number> = (v) => (typeof v === "number" && Number.isInteger(v) ? v : null);
export const str: Decoder<string> = (v) => (typeof v === "string" ? v : null);
export const bool: Decoder<boolean> = (v) => (typeof v === "boolean" ? v : null);

/** Optional field: missing / null become the fallback. */
export const opt =
  <T>(d: Decoder<T>, fallback: T): Decoder<T> =>
  (v) =>
    v === undefined || v === null ? fallback : d(v);

export const arr =
  <T>(d: Decoder<T>, max = 100_000): Decoder<T[]> =>
  (v) => {
    if (!Array.isArray(v) || v.length > max) return null;
    const out: T[] = [];
    for (const x of v) {
      const y = d(x);
      if (y === null) return null;
      out.push(y);
    }
    return out;
  };

type Shape = Record<string, Decoder<unknown>>;
type Out<S extends Shape> = { [K in keyof S]: S[K] extends Decoder<infer T> ? T : never };

/** Object decoder: every listed field must decode; extra fields are ignored
 * (forward compatibility with newer servers). */
export const obj =
  <S extends Shape>(shape: S): Decoder<Out<S>> =>
  (v) => {
    if (!isObj(v)) return null;
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(shape)) {
      const d = shape[k];
      if (!d) return null;
      const y = d(v[k]);
      if (y === null) return null;
      out[k] = y;
    }
    return out as Out<S>;
  };

export const oneOf =
  <T extends string>(...vals: T[]): Decoder<T> =>
  (v) =>
    typeof v === "string" && (vals as string[]).includes(v) ? (v as T) : null;

export const tuple2 =
  <A, B>(a: Decoder<A>, b: Decoder<B>): Decoder<[A, B]> =>
  (v) => {
    if (!Array.isArray(v) || v.length !== 2) return null;
    const x = a(v[0]);
    const y = b(v[1]);
    return x === null || y === null ? null : [x, y];
  };
