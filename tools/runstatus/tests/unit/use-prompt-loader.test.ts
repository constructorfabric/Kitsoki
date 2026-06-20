import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ref, computed } from 'vue';
import { usePromptLoader } from '../../src/components/agent/usePromptLoader.js';

describe('usePromptLoader', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns inline prompt if available', () => {
    const attrs = computed(() => ({
      prompt: 'inline prompt text',
      system_prompt: 'inline system prompt',
    }));

    const { prompt, systemPrompt } = usePromptLoader(attrs);

    expect(prompt.value).toBe('inline prompt text');
    expect(systemPrompt.value).toBe('inline system prompt');
  });

  it('loads prompt from file if prompt_file is set and prompt is empty', async () => {
    const mockFetch = vi.fn().mockResolvedValue(
      new Response('loaded prompt from file')
    );
    global.fetch = mockFetch;

    const attrs = computed(() => ({
      prompt: '',
      prompt_file: '/path/to/prompt.txt',
      system_prompt: 'inline system',
    }));

    const { prompt, systemPrompt } = usePromptLoader(attrs);

    // Wait for the watcher to load the file
    await new Promise(resolve => setTimeout(resolve, 50));

    expect(mockFetch).toHaveBeenCalledWith('/path/to/prompt.txt');
    expect(prompt.value).toBe('loaded prompt from file');
    expect(systemPrompt.value).toBe('inline system');
  });

  it('prioritizes inline prompt over prompt_file', () => {
    const attrs = computed(() => ({
      prompt: 'inline prompt',
      prompt_file: '/should/not/load',
    }));

    const { prompt } = usePromptLoader(attrs);

    expect(prompt.value).toBe('inline prompt');
  });

  it('handles fetch errors gracefully', async () => {
    const mockFetch = vi.fn().mockRejectedValue(new Error('Network error'));
    global.fetch = mockFetch;

    const attrs = computed(() => ({
      prompt: '',
      prompt_file: '/path/to/missing.txt',
    }));

    const { prompt } = usePromptLoader(attrs);

    await new Promise(resolve => setTimeout(resolve, 50));

    expect(prompt.value).toContain('[Prompt file: /path/to/missing.txt - could not load]');
  });

  it('handles 404 responses gracefully', async () => {
    const mockFetch = vi.fn().mockResolvedValue(
      new Response('Not Found', { status: 404 })
    );
    global.fetch = mockFetch;

    const attrs = computed(() => ({
      prompt: '',
      prompt_file: '/nonexistent.txt',
    }));

    const { prompt } = usePromptLoader(attrs);

    await new Promise(resolve => setTimeout(resolve, 50));

    expect(prompt.value).toContain('[Prompt file: /nonexistent.txt - could not load]');
  });

  it('returns empty string if neither prompt nor prompt_file is set', () => {
    const attrs = computed(() => ({
      prompt: '',
      prompt_file: '',
    }));

    const { prompt, systemPrompt } = usePromptLoader(attrs);

    expect(prompt.value).toBe('');
    expect(systemPrompt.value).toBe('');
  });
});
