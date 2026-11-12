export const hm = (d: Date | string) => {
  const x = typeof d === 'string' ? new Date(d) : d;
  return String(x.getHours()).padStart(2, '0') + ':' + String(x.getMinutes()).padStart(2, '0');
};
/** 550 -> "9 h 10 min" */
export const durMin = (m: number) => `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')} min`;
export const mmss = (s: number) => String(Math.floor(s / 60)).padStart(2, '0') + ':' + String(s % 60).padStart(2, '0');
export const initials = (n: string) => n.split(' ').map((w) => w[0]).join('').slice(0, 2).toUpperCase();
