// Decision Stack — Text Input Field for One-Line Decisions
//
// Features:
// - Large, centered multi-line text input
// - Auto-focus on mount
// - Character count with soft limit (200 chars)
// - Rotating example prompts that cycle every 3s
// - Voice input button (placeholder for future voice integration)
// - Clean, minimal design optimized for quick decision entry

import React, { useState, useEffect, useRef, useCallback } from "react";
import {
  View,
  TextInput,
  Text,
  TouchableOpacity,
  StyleSheet,
  Keyboard,
} from "react-native";
import { Colors, Type, Space, CardDim } from "../../styles/cardStyles";

// Example prompts that rotate to give users ideas
const EXAMPLE_PROMPTS = [
  "Tell Sarah 9500, 2 weeks",
  "Accept the meeting Thursday",
  "Forward to accounting",
  "Decline — budget constraints",
  "Ask for more details on timeline",
  "Approve with standard terms",
  "Counter at 8000, 3 week delivery",
  "Schedule call for next Tuesday",
];

const SOFT_LIMIT = 200;

export interface TextInputFieldProps {
  value: string;
  onChangeText: (text: string) => void;
  onSubmit?: () => void;
  placeholder?: string;
  autoFocus?: boolean;
  editable?: boolean;
}

/**
 * TextInputField — Large decision input with rotating examples
 */
export const TextInputField: React.FC<TextInputFieldProps> = ({
  value,
  onChangeText,
  onSubmit,
  placeholder,
  autoFocus = true,
  editable = true,
}) => {
  const [currentExampleIndex, setCurrentExampleIndex] = useState(0);
  const [isFocused, setIsFocused] = useState(false);
  const inputRef = useRef<TextInput>(null);

  // Rotate example prompts every 3 seconds when input is empty and not focused
  useEffect(() => {
    if (value.length > 0 || isFocused) return;

    const interval = setInterval(() => {
      setCurrentExampleIndex((prev) => (prev + 1) % EXAMPLE_PROMPTS.length);
    }, 3000);

    return () => clearInterval(interval);
  }, [value.length, isFocused]);

  // Auto-focus on mount
  useEffect(() => {
    if (autoFocus && editable) {
      const timer = setTimeout(() => {
        inputRef.current?.focus();
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [autoFocus, editable]);

  const charCount = value.length;
  const nearLimit = charCount > SOFT_LIMIT * 0.8;
  const atLimit = charCount >= SOFT_LIMIT;

  const displayPlaceholder =
    placeholder || EXAMPLE_PROMPTS[currentExampleIndex];

  const handleVoicePress = useCallback(() => {
    // Placeholder for voice integration
    // Will integrate with VoiceMode in future iteration
    console.log("[TextInputField] Voice input requested");
  }, []);

  const handleSubmitEditing = useCallback(() => {
    if (onSubmit && value.trim().length > 0 && !atLimit) {
      onSubmit();
    }
  }, [onSubmit, value, atLimit]);

  return (
    <View style={styles.container}>
      {/* Input area */}
      <View
        style={[
          styles.inputContainer,
          isFocused && styles.inputContainerFocused,
        ]}
      >
        <TextInput
          ref={inputRef}
          style={styles.input}
          value={value}
          onChangeText={(text) => {
            // Enforce soft limit
            if (text.length <= SOFT_LIMIT + 20) {
              onChangeText(text);
            }
          }}
          onFocus={() => setIsFocused(true)}
          onBlur={() => setIsFocused(false)}
          onSubmitEditing={handleSubmitEditing}
          placeholder={isFocused ? "" : displayPlaceholder}
          placeholderTextColor={Colors.textTertiary}
          multiline
          numberOfLines={3}
          maxLength={SOFT_LIMIT + 20}
          textAlignVertical="center"
          textAlign="center"
          returnKeyType="send"
          blurOnSubmit={false}
          editable={editable}
          accessibilityLabel="Decision input"
          accessibilityHint="Type a one-line instruction for how to handle this request"
        />

        {/* Voice input button */}
        <TouchableOpacity
          style={styles.voiceButton}
          onPress={handleVoicePress}
          activeOpacity={0.7}
          accessibilityLabel="Voice input"
          accessibilityHint="Tap to speak your decision"
        >
          <Text style={styles.voiceIcon}>🎤</Text>
        </TouchableOpacity>
      </View>

      {/* Character count */}
      <View style={styles.footer}>
        <Text
          style={[
            styles.charCount,
            nearLimit && styles.charCountNear,
            atLimit && styles.charCountAt,
          ]}
        >
          {`${charCount}/${SOFT_LIMIT}`}
        </Text>
      </View>

      {/* Hint text */}
      {!isFocused && value.length === 0 && (
        <Text style={styles.hint}>
          Type a quick instruction, or tap a suggestion below
        </Text>
      )}
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    width: "100%",
    alignItems: "center",
  },
  inputContainer: {
    width: "100%",
    minHeight: 100,
    backgroundColor: Colors.bgCard,
    borderRadius: 16,
    borderWidth: 1.5,
    borderColor: Colors.border,
    paddingHorizontal: Space.lg,
    paddingVertical: Space.md,
    position: "relative",
    justifyContent: "center",
  },
  inputContainerFocused: {
    borderColor: Colors.primary,
    borderWidth: 2,
    shadowColor: Colors.primary,
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.1,
    shadowRadius: 8,
    elevation: 2,
  },
  input: {
    ...Type.bodyLarge,
    color: Colors.textMain,
    textAlign: "center",
    minHeight: 80,
    paddingRight: Space.xl + Space.sm, // Room for voice button
    lineHeight: 24,
  },
  voiceButton: {
    position: "absolute",
    right: Space.md,
    bottom: Space.md,
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: Colors.bgAccent,
    justifyContent: "center",
    alignItems: "center",
  },
  voiceIcon: {
    fontSize: 16,
  },
  footer: {
    flexDirection: "row",
    justifyContent: "flex-end",
    width: "100%",
    marginTop: Space.sm,
    paddingHorizontal: Space.xs,
  },
  charCount: {
    ...Type.micro,
    color: Colors.textTertiary,
  },
  charCountNear: {
    color: Colors.urgentOrange,
  },
  charCountAt: {
    color: Colors.urgentRed,
    fontWeight: "600",
  },
  hint: {
    ...Type.caption,
    color: Colors.textTertiary,
    textAlign: "center",
    marginTop: Space.md,
  },
});
