import React, { forwardRef } from "react";
import { View, Text, TouchableOpacity } from "react-native";
import { cardStyles } from "../../styles/cardStyles";

export interface CardActionsProps {
  onDecide: () => void;
  onConsult: () => void;
  onSource: () => void;
  onSkip: () => void;
}

/**
 * CardActions — Horizontal button row at bottom of card
 * [Decide]  [Consult]  [Source]  [Skip]
 *  primary   secondary  text      minimal
 *
 * Refs are exposed for tutorial spotlight targeting:
 *   - decideRef: wraps the Decide (primary) button → "decision_input" / "approve_button" target
 *   - sourceRef: wraps the Source text button → "source_button" target
 */
export const CardActions = forwardRef<View, CardActionsProps>(
  ({ onDecide, onConsult, onSource, onSkip }, decideRef) => {
    return (
      <View style={cardStyles.actionRow}>
        {/* Primary: Decide — ref attached for tutorial targeting */}
        <View ref={decideRef} collapsable={false}>
          <TouchableOpacity
            style={cardStyles.actionBtnPrimary}
            onPress={onDecide}
            activeOpacity={0.8}
          >
            <Text style={cardStyles.actionBtnPrimaryText}>Decide</Text>
          </TouchableOpacity>
        </View>

        {/* Secondary: Consult */}
        <TouchableOpacity
          style={cardStyles.actionBtnSecondary}
          onPress={onConsult}
          activeOpacity={0.8}
        >
          <Text style={cardStyles.actionBtnSecondaryText}>Consult</Text>
        </TouchableOpacity>

        {/* Text button: Source — wrapped in View for tutorial ref targeting */}
        <TouchableOpacity onPress={onSource} activeOpacity={0.7}>
          <Text style={cardStyles.actionBtnText}>Source</Text>
        </TouchableOpacity>

        {/* Minimal: Skip (right-aligned spacer) */}
        <TouchableOpacity
          style={cardStyles.actionBtnSkip}
          onPress={onSkip}
          activeOpacity={0.7}
        >
          <Text style={cardStyles.actionBtnSkipText}>Skip</Text>
        </TouchableOpacity>
      </View>
    );
  }
);

CardActions.displayName = "CardActions";
