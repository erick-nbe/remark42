import { Comment } from 'common/types';
import { EXPANDED_REPLIES_SET, EXPANDED_REPLIES_TOGGLE } from './types';

export const setExpandedReplies = (id: Comment['id'], expanded: boolean) => ({
  type: EXPANDED_REPLIES_SET,
  id,
  expanded,
});

export const toggleExpandedReplies = (id: Comment['id']) => ({
  type: EXPANDED_REPLIES_TOGGLE,
  id,
});
