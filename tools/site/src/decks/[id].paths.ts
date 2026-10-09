import { loadDecks } from "../../.vitepress/data/decks.js";

export default {
  paths() {
    return loadDecks().map((deck) => ({
      params: deck,
    }));
  },
};
