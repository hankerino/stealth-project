const usd = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });

export const cents = (c: number | null | undefined) => (c == null ? "—" : usd.format(c / 100));
export const when = (iso: string | number) =>
  new Date(iso).toLocaleString(undefined, { hour12: false });
