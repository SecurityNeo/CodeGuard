// theme.js — 主题切换：暗色 / 亮色 / 系统跟随
// localStorage 键: 'theme' ∈ { 'light', 'dark', 'system' }
// 默认: 'system' (跟随 prefers-color-scheme)

(function () {
  'use strict';

  const STORAGE_KEY = 'cg.theme';
  const VALID = ['light', 'dark', 'system'];

  function getStoredTheme() {
    try {
      const v = localStorage.getItem(STORAGE_KEY);
      if (VALID.includes(v)) return v;
    } catch (e) { /* localStorage 可能被禁用 */ }
    return 'system';
  }

  function setStoredTheme(v) {
    try {
      localStorage.setItem(STORAGE_KEY, v);
    } catch (e) { /* 静默失败 */ }
  }

  function resolveTheme(pref) {
    if (pref === 'dark' || pref === 'light') return pref;
    if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
      return 'dark';
    }
    return 'light';
  }

  function applyTheme(pref) {
    const resolved = resolveTheme(pref);
    document.documentElement.classList.toggle('dark', resolved === 'dark');
    document.documentElement.dataset.themePref = pref;
    document.documentElement.dataset.themeResolved = resolved;
  }

  // 暴露全局 API
  window.ThemeManager = {
    /** 获取用户偏好（未设置时返回 'system'） */
    getPref: getStoredTheme,
    /** 设置偏好并立即应用 */
    setPref(pref) {
      if (!VALID.includes(pref)) return;
      setStoredTheme(pref);
      applyTheme(pref);
      // 通知所有图表组件重新渲染（图表主题可能需要切换）
      document.dispatchEvent(new CustomEvent('themechange', { detail: { pref, resolved: resolveTheme(pref) } }));
    },
    /** 当前实际应用的主题（'light' / 'dark'） */
    getResolved() {
      return document.documentElement.classList.contains('dark') ? 'dark' : 'light';
    },
    /** 初始化：在所有内容渲染前应用主题，避免 FOUC */
    init() {
      applyTheme(getStoredTheme());
      // 监听系统主题变化（仅当 pref='system' 时生效）
      if (window.matchMedia) {
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
          if (getStoredTheme() === 'system') {
            applyTheme('system');
            document.dispatchEvent(new CustomEvent('themechange', { detail: { pref: 'system', resolved: resolveTheme('system') } }));
          }
        });
      }
    }
  };

  // 立即初始化（在 DOMContentLoaded 之前，避免 flash）
  window.ThemeManager.init();
})();
