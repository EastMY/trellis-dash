import "@testing-library/jest-dom/vitest";

// 测试环境统一使用内存 localStorage，避免 Node 未配置持久化文件时产生环境差异。
const localValues = new Map<string, string>();
const localStorageMock: Storage = {
  get length() { return localValues.size; },
  clear: () => localValues.clear(),
  getItem: (key) => localValues.get(key) ?? null,
  key: (index) => [...localValues.keys()][index] ?? null,
  removeItem: (key) => { localValues.delete(key); },
  setItem: (key, value) => { localValues.set(key, value); },
};
Object.defineProperty(window, "localStorage", { configurable: true, value: localStorageMock });
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: localStorageMock });

const nativeGetComputedStyle = window.getComputedStyle.bind(window);
// jsdom 不支持伪元素样式参数；Ant Design 只用它测量滚动条，忽略该参数即可。
window.getComputedStyle = (element: Element) => nativeGetComputedStyle(element);

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  }),
});

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(window, "ResizeObserver", {
  writable: true,
  value: ResizeObserverMock,
});
