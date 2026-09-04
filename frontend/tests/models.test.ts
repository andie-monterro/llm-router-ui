import assert from 'node:assert/strict';
import test from 'node:test';
import { filterModels } from '../src/lib/models.ts';

test('filterModels deduplicates, searches, and keeps auto first', () => {
  assert.deepEqual(filterModels(['z-model', 'auto', 'a-model', 'auto'], ''), ['auto', 'a-model', 'z-model']);
  assert.deepEqual(filterModels(['gpt-5.6-sol', 'deepseek-v4-flash'], 'GPT'), ['gpt-5.6-sol']);
});
