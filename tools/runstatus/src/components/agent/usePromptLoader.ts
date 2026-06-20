import { computed, ref, watch } from "vue";

export function usePromptLoader(attrs: any) {
  const loadedPrompt = ref<string>("");
  const loadedSystemPrompt = ref<string>("");

  const loadPromptFromFile = async (promptFile: string) => {
    try {
      const response = await fetch(String(promptFile));
      if (response.ok) {
        return await response.text();
      }
    } catch (e) {
      console.warn(`Failed to load prompt from ${promptFile}:`, e);
    }
    return `[Prompt file: ${promptFile} - could not load]`;
  };

  watch(() => attrs.value?.prompt_file, async (promptFile) => {
    if (promptFile && !attrs.value?.prompt) {
      loadedPrompt.value = await loadPromptFromFile(String(promptFile));
    }
  }, { immediate: true });

  watch(() => attrs.value?.system_prompt_file, async (systemPromptFile) => {
    if (systemPromptFile && !attrs.value?.system_prompt) {
      loadedSystemPrompt.value = await loadPromptFromFile(String(systemPromptFile));
    }
  }, { immediate: true });

  const prompt = computed(() => {
    const p = attrs.value?.prompt;
    if (p) return String(p);
    return loadedPrompt.value;
  });

  const systemPrompt = computed(() => {
    const sp = attrs.value?.system_prompt;
    if (sp) return String(sp);
    return loadedSystemPrompt.value;
  });

  return { prompt, systemPrompt };
}
