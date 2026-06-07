import React from "react";
import {
  View,
  Text,
  TouchableOpacity,
  ScrollView,
  SafeAreaView,
  StyleSheet,
} from "react-native";
import { cardStyles, Colors, Type, Space } from "../styles/cardStyles";
import type { ChunkCitation } from "../types/cards";

interface SourceViewerScreenProps {
  citations: ChunkCitation[];
  currentIndex: number;
  onBack: () => void;
  onChangeIndex?: (index: number) => void;
}

/**
 * SourceViewerScreen — Verbatim citation viewer
 *
 * Triggered by [Source] tap on a card.
 * Shows the verbatim snippet with full citation metadata.
 * Scrollable if multiple citations.
 *
 * Format:
 *   Source 1 of 3
 *   ─────────────────
 *   "The updated proposal includes..."
 *
 *   — chunk_id: abc-123
 *   — email_id: def-456
 *
 *   [Back to Card]
 */
export const SourceViewerScreen: React.FC<SourceViewerScreenProps> = ({
  citations,
  currentIndex,
  onBack,
  onChangeIndex,
}) => {
  if (!citations || citations.length === 0) {
    return (
      <SafeAreaView style={styles.container}>
        <View style={styles.emptyState}>
          <Text style={styles.emptyText}>No sources available.</Text>
          <TouchableOpacity
            style={styles.backButton}
            onPress={onBack}
            activeOpacity={0.8}
          >
            <Text style={styles.backButtonText}>Back to Card</Text>
          </TouchableOpacity>
        </View>
      </SafeAreaView>
    );
  }

  const total = citations.length;
  const citation = citations[currentIndex];

  return (
    <SafeAreaView style={styles.container}>
      {/* Header with navigation between sources */}
      <View style={styles.header}>
        <Text style={styles.headerText}>
          {`Source ${currentIndex + 1} of ${total}`}
        </Text>

        {/* Dot indicators for multiple citations */}
        {total > 1 && (
          <View style={styles.dotRow}>
            {citations.map((_, idx) => (
              <TouchableOpacity
                key={idx}
                onPress={() => onChangeIndex?.(idx)}
                activeOpacity={0.7}
              >
                <View
                  style={[
                    styles.dot,
                    idx === currentIndex && styles.dotActive,
                  ]}
                />
              </TouchableOpacity>
            ))}
          </View>
        )}
      </View>

      <ScrollView
        style={styles.scroll}
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {/* Divider */}
        <View style={styles.divider} />

        {/* Verbatim quote */}
        <Text style={styles.quoteMark}>"</Text>
        <Text style={styles.quoteText}>{citation.verbatim_snippet}</Text>
        <Text style={styles.quoteMarkClosing}>"</Text>

        {/* Metadata */}
        <View style={styles.metaContainer}>
          <View style={styles.metaRow}>
            <Text style={styles.metaBullet}>—</Text>
            <Text style={styles.metaText}>
              <Text style={styles.metaLabel}>chunk_id: </Text>
              {citation.chunk_id}
            </Text>
          </View>
          <View style={styles.metaRow}>
            <Text style={styles.metaBullet}>—</Text>
            <Text style={styles.metaText}>
              <Text style={styles.metaLabel}>email_id: </Text>
              {citation.email_id}
            </Text>
          </View>
          <View style={styles.metaRow}>
            <Text style={styles.metaBullet}>—</Text>
            <Text style={styles.metaText}>
              <Text style={styles.metaLabel}>paragraph: </Text>
              {citation.paragraph_index + 1}
            </Text>
          </View>
        </View>

        {/* Back button */}
        <TouchableOpacity
          style={styles.backButton}
          onPress={onBack}
          activeOpacity={0.8}
        >
          <Text style={styles.backButtonText}>Back to Card</Text>
        </TouchableOpacity>
      </ScrollView>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: Colors.bgWarm,
  },
  emptyState: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  emptyText: {
    ...Type.body,
    color: Colors.textTertiary,
    marginBottom: Space.lg,
  },
  header: {
    paddingTop: Space.lg,
    paddingBottom: Space.sm,
    alignItems: "center",
  },
  headerText: {
    ...Type.captionBold,
    color: Colors.textTertiary,
  },
  dotRow: {
    flexDirection: "row",
    justifyContent: "center",
    marginTop: Space.sm,
    gap: Space.sm,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: Colors.border,
  },
  dotActive: {
    backgroundColor: Colors.primary,
  },
  scroll: {
    flex: 1,
  },
  scrollContent: {
    padding: Space.lg,
    paddingTop: 0,
  },
  divider: {
    height: 1,
    backgroundColor: Colors.border,
    marginBottom: Space.lg,
  },
  quoteMark: {
    ...Type.display,
    color: Colors.textTertiary,
    opacity: 0.4,
    lineHeight: 28,
    marginBottom: -Space.sm,
  },
  quoteMarkClosing: {
    ...Type.display,
    color: Colors.textTertiary,
    opacity: 0.4,
    lineHeight: 28,
    marginTop: -Space.sm,
    textAlign: "right",
  },
  quoteText: {
    ...Type.body,
    color: Colors.textMain,
    lineHeight: 26,
    fontStyle: "italic",
    paddingHorizontal: Space.sm,
  },
  metaContainer: {
    marginTop: Space.lg,
    gap: Space.xs,
  },
  metaRow: {
    flexDirection: "row",
    alignItems: "flex-start",
  },
  metaBullet: {
    ...Type.caption,
    color: Colors.textTertiary,
    width: 20,
  },
  metaText: {
    ...Type.caption,
    color: Colors.textSecondary,
    flex: 1,
  },
  metaLabel: {
    ...Type.captionBold,
    color: Colors.textTertiary,
  },
  backButton: {
    marginTop: Space.xl,
    alignSelf: "center",
    paddingVertical: Space.sm,
    paddingHorizontal: Space.lg,
    borderRadius: 12,
    backgroundColor: Colors.bgCard,
    borderWidth: 1,
    borderColor: Colors.border,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 2,
  },
  backButtonText: {
    ...Type.captionBold,
    color: Colors.textSecondary,
  },
});
