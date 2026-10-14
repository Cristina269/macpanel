// MacPanel i18n runtime. English is the source language: UI strings in the
// markup and in app.js are English literals used as dictionary keys, and the
// zh dictionary (i18n-zh.js, loaded after this file, before app.js) maps them
// to Chinese. Default language is English; the toggle persists the choice.
window.I18N = {
  lang: localStorage.getItem('mp-lang') || 'en',
  dict: null, // { 'English source': '中文翻译' }
  t(s) {
    if (this.lang !== 'zh' || !this.dict) return s;
    return this.dict[s] || s;
  },
  set(lang) {
    localStorage.setItem('mp-lang', lang);
    location.reload();
  },
  // Static shell only (index.html): elements marked data-i18n carry the
  // English source in the attribute; dynamic views wrap their strings in t()
  // at render time.
  applyStatic(root) {
    root = root || document;
    root.querySelectorAll('[data-i18n]').forEach(el => {
      el.textContent = this.t(el.getAttribute('data-i18n'));
    });
    root.querySelectorAll('[data-i18n-ph]').forEach(el => {
      el.setAttribute('placeholder', this.t(el.getAttribute('data-i18n-ph')));
    });
    document.querySelectorAll('#lang-toggle, #lang-toggle-float').forEach(btn => {
      btn.textContent = this.lang === 'en' ? '中文' : 'EN';
    });
    document.documentElement.lang = this.lang === 'en' ? 'en' : 'zh-CN';
  }
};
window.t = s => I18N.t(s);
document.addEventListener('DOMContentLoaded', () => I18N.applyStatic());
