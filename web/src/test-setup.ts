import { loadCatalog } from './lib/i18n/core';
// translations load lazily in the app; tests switch languages synchronously
await loadCatalog();
