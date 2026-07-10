import { beforeEach } from "vitest";

function createMemoryStorage(): Storage {
  const data = new Map<string, string>();
  return {
    get length(): number {
      return data.size;
    },
    clear(): void {
      data.clear();
    },
    getItem(key: string): string | null {
      return data.has(key) ? data.get(key)! : null;
    },
    key(index: number): string | null {
      return Array.from(data.keys())[index] ?? null;
    },
    removeItem(key: string): void {
      data.delete(key);
    },
    setItem(key: string, value: string): void {
      data.set(key, String(value));
    },
  };
}

const local = createMemoryStorage();
const session = createMemoryStorage();

function installStorage(name: "localStorage" | "sessionStorage", value: Storage): void {
  Object.defineProperty(globalThis, name, {
    value,
    configurable: true,
    writable: true,
  });
  if (typeof window !== "undefined") {
    Object.defineProperty(window, name, {
      value,
      configurable: true,
      writable: true,
    });
  }
}

installStorage("localStorage", local);
installStorage("sessionStorage", session);

beforeEach(() => {
  local.clear();
  session.clear();
});
