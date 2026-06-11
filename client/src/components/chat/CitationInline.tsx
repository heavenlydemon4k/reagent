// Decision Stack — Inline Citation Chips
// Small clickable chips rendered below assistant messages for source attribution

import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import type { ChunkCitation } from '../../types/cards';

interface CitationInlineProps {
  citations: ChunkCitation[];
  onPressCitation: (citation: ChunkCitation) => void;
}

/**
 * CitationInline — row of small, tappable citation chips.
 * Each chip shows a snippet preview and opens the source email on tap.
 */
export const CitationInline: React.FC<CitationInlineProps> = ({
  citations,
  onPressCitation,
}) => {
  if (!citations || citations.length === 0) return null;

  return (
    <View style={styles.container}>
      {citations.map((citation, index) => {
        // Truncate snippet for display
        const snippet = citation.verbatim_snippet;
        const displayText =
          snippet.length > 30 ? snippet.slice(0, 30) + '…' : snippet;

        return (
          <TouchableOpacity
            key={citation.chunk_id}
            onPress={() => onPressCitation(citation)}
            style={styles.chip}
            activeOpacity={0.7}
            accessibilityLabel={`Citation ${index + 1}: ${displayText}`}
            accessibilityRole="button"
          >
            <View style={styles.chipDot} />
            <Text style={styles.chipText}>{displayText}</Text>
          </TouchableOpacity>
        );
      })}
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: spacing[1.5],
  },
  chip: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: palette.steel[50],
    borderRadius: spacing[2],
    paddingHorizontal: spacing[2.5],
    paddingVertical: spacing[1],
    borderWidth: 1,
    borderColor: palette.steel[200],
  },
  chipDot: {
    width: spacing[1.5],
    height: spacing[1.5],
    borderRadius: spacing[0.75],
    backgroundColor: palette.steel[400],
    marginRight: spacing[1.5],
  },
  chipText: {
    fontSize: fontSize.xs,
    color: palette.steel[700],
    fontWeight: fontWeight.medium,
  },
});
