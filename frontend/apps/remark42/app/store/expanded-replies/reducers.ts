import { EXPANDED_REPLIES_SET, EXPANDED_REPLIES_TOGGLE, EXPANDED_REPLIES_ACTIONS } from './types';

export interface ExpandedRepliesState {
  [key: string]: boolean;
}

export function expandedReplies(state: ExpandedRepliesState = {}, action: EXPANDED_REPLIES_ACTIONS): ExpandedRepliesState {
  switch (action.type) {
    case EXPANDED_REPLIES_SET: {
      return {
        ...state,
        [action.id]: action.expanded,
      };
    }
    case EXPANDED_REPLIES_TOGGLE: {
      return {
        ...state,
        [action.id]: !state[action.id],
      };
    }
    default:
      return state;
  }
}
