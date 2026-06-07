// Decision Stack — Schedule Send Modal
//
// Shown after the user taps "Approve" on a draft. Lets them choose
// between sending immediately or scheduling for a future time.
//
// Presets:
//   - Send now
//   - Tomorrow 9am
//   - Monday 9am
//   - Custom (date + time picker in user's local timezone)
//
// All times are converted to UTC before sending to the API.

import React, { useState, useCallback, useMemo } from "react";
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Modal,
  Platform,
  ScrollView,
} from "react-native";

import { Colors, Type, Space, CardDim } from "../../styles/cardStyles";

// ─── Types ─────────────────────────────────────────────────────────────────

export type SchedulePreset = "now" | "tomorrow_9am" | "monday_9am" | "custom";

export interface ScheduleSendModalProps {
  visible: boolean;
  onClose: () => void;
  onSendNow: () => void;
  onSchedule: (utcISO: string, displayLabel: string) => void;
  userTimezone?: string; // e.g. "America/New_York"; falls back to device
}

interface PresetOption {
  key: SchedulePreset;
  label: string;
  sublabel: string;
  icon: string;
}

// ─── Helpers ───────────────────────────────────────────────────────────────

/**
 * Get the user's local timezone (fallback for userTimezone prop).
 */
function getDeviceTimezone(): string {
  if (typeof Intl !== "undefined" && Intl.DateTimeFormat) {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  }
  return "UTC";
}

/**
 * Build preset options based on current local time.
 */
function buildPresets(tz: string): PresetOption[] {
  const now = new Date();

  // Tomorrow 9am
  const tomorrow = new Date(now);
  tomorrow.setDate(tomorrow.getDate() + 1);
  tomorrow.setHours(9, 0, 0, 0);

  // Monday 9am (if today is Monday, next Monday)
  const day = now.getDay();
  const daysUntilMonday = day === 1 ? 7 : (8 - day) % 7 || 7;
  const monday = new Date(now);
  monday.setDate(monday.getDate() + daysUntilMonday);
  monday.setHours(9, 0, 0, 0);

  const fmt = (d: Date) =>
    d.toLocaleDateString(undefined, {
      weekday: "short",
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
      timeZone: tz,
    });

  return [
    {
      key: "now",
      label: "Send now",
      sublabel: "Deliver immediately",
      icon: "▸",
    },
    {
      key: "tomorrow_9am",
      label: "Tomorrow 9am",
      sublabel: fmt(tomorrow),
      icon: "☀",
    },
    {
      key: "monday_9am",
      label: "Monday 9am",
      sublabel: fmt(monday),
      icon: "M",
    },
    {
      key: "custom",
      label: "Custom time",
      sublabel: "Pick your own date & time",
      icon: "◷",
    },
  ];
}

/**
 * Convert a local date (from pickers) to UTC ISO string for the API.
 */
function localToUTCISO(year: number, month: number, day: number, hour: number, minute: number): string {
  // Create date in local time, then get UTC equivalent
  const local = new Date(year, month, day, hour, minute, 0);
  return local.toISOString();
}

/**
 * Parse a UTC ISO string into local date components for the pickers.
 */
function utcISOToLocal(iso: string): {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
} {
  const d = new Date(iso);
  return {
    year: d.getFullYear(),
    month: d.getMonth(),
    day: d.getDate(),
    hour: d.getHours(),
    minute: d.getMinutes(),
  };
}

// ─── Component ─────────────────────────────────────────────────────────────

/**
 * ScheduleSendModal — Post-approval send-time selector
 *
 * Usage:
 * ```tsx
 * <ScheduleSendModal
 *   visible={showSchedule}
 *   onClose={() => setShowSchedule(false)}
 *   onSendNow={handleImmediateSend}
 *   onSchedule={(utcISO, label) => handleSchedule(utcISO, label)}
 * />
 * ```
 */
export const ScheduleSendModal: React.FC<ScheduleSendModalProps> = ({
  visible,
  onClose,
  onSendNow,
  onSchedule,
  userTimezone,
}) => {
  const tz = userTimezone || getDeviceTimezone();
  const presets = useMemo(() => buildPresets(tz), [tz]);

  const [selectedPreset, setSelectedPreset] = useState<SchedulePreset>("now");

  // Custom date/time state
  const now = new Date();
  const [customYear, setCustomYear] = useState(now.getFullYear());
  const [customMonth, setCustomMonth] = useState(now.getMonth());
  const [customDay, setCustomDay] = useState(now.getDate());
  const [customHour, setCustomHour] = useState(9);
  const [customMinute, setCustomMinute] = useState(0);

  const handleConfirm = useCallback(() => {
    if (selectedPreset === "now") {
      onSendNow();
      return;
    }

    let utcISO: string;
    let displayLabel: string;

    switch (selectedPreset) {
      case "tomorrow_9am": {
        const tomorrow = new Date();
        tomorrow.setDate(tomorrow.getDate() + 1);
        utcISO = localToUTCISO(
          tomorrow.getFullYear(),
          tomorrow.getMonth(),
          tomorrow.getDate(),
          9,
          0
        );
        displayLabel = "Tomorrow 9am";
        break;
      }
      case "monday_9am": {
        const day = new Date().getDay();
        const daysUntilMonday = day === 1 ? 7 : (8 - day) % 7 || 7;
        const monday = new Date();
        monday.setDate(monday.getDate() + daysUntilMonday);
        utcISO = localToUTCISO(
          monday.getFullYear(),
          monday.getMonth(),
          monday.getDate(),
          9,
          0
        );
        displayLabel = "Monday 9am";
        break;
      }
      case "custom": {
        utcISO = localToUTCISO(customYear, customMonth, customDay, customHour, customMinute);
        const d = new Date(utcISO);
        displayLabel = d.toLocaleString(undefined, {
          weekday: "short",
          month: "short",
          day: "numeric",
          hour: "numeric",
          minute: "2-digit",
          timeZone: tz,
        });
        break;
      }
      default:
        return;
    }

    onSchedule(utcISO, displayLabel);
  }, [
    selectedPreset,
    customYear,
    customMonth,
    customDay,
    customHour,
    customMinute,
    onSendNow,
    onSchedule,
    tz,
  ]);

  // ─── Preset row renderer ──────────────────────────────────────────

  const renderPreset = (preset: PresetOption) => {
    const isSelected = selectedPreset === preset.key;
    return (
      <TouchableOpacity
        key={preset.key}
        style={[styles.presetRow, isSelected && styles.presetRowSelected]}
        onPress={() => setSelectedPreset(preset.key)}
        activeOpacity={0.8}
        accessibilityLabel={preset.label}
        accessibilityState={{ selected: isSelected }}
      >
        <View style={styles.presetIconWrap}>
          <Text style={styles.presetIcon}>{preset.icon}</Text>
        </View>
        <View style={styles.presetTextWrap}>
          <Text
            style={[
              styles.presetLabel,
              isSelected && styles.presetLabelSelected,
            ]}
          >
            {preset.label}
          </Text>
          <Text style={styles.presetSublabel}>{preset.sublabel}</Text>
        </View>
        <View style={styles.presetRadio}>
          <View
            style={[
              styles.presetRadioOuter,
              isSelected && styles.presetRadioOuterSelected,
            ]}
          >
            {isSelected && <View style={styles.presetRadioInner} />}
          </View>
        </View>
      </TouchableOpacity>
    );
  };

  // ─── Custom picker ────────────────────────────────────────────────

  const renderCustomPicker = () => {
    if (selectedPreset !== "custom") return null;

    // Generate arrays for pickers
    const years = Array.from({ length: 2 }, (_, i) => now.getFullYear() + i);
    const months = [
      "January", "February", "March", "April", "May", "June",
      "July", "August", "September", "October", "November", "December",
    ];
    const daysInMonth = new Date(customYear, customMonth + 1, 0).getDate();
    const days = Array.from({ length: daysInMonth }, (_, i) => i + 1);
    const hours = Array.from({ length: 24 }, (_, i) => i);
    const minutes = [0, 15, 30, 45];

    return (
      <View style={styles.customWrap}>
        <Text style={styles.customLabel}>Date & Time ({tz})</Text>

        {/* Date row */}
        <View style={styles.pickerRow}>
          <View style={styles.pickerCol}>
            <Text style={styles.pickerLabel}>Month</Text>
            <ScrollView style={styles.pickerScroll} nestedScrollEnabled>
              {months.map((m, idx) => (
                <TouchableOpacity
                  key={m}
                  style={[
                    styles.pickerItem,
                    customMonth === idx && styles.pickerItemActive,
                  ]}
                  onPress={() => setCustomMonth(idx)}
                >
                  <Text
                    style={[
                      styles.pickerItemText,
                      customMonth === idx && styles.pickerItemTextActive,
                    ]}
                  >
                    {m.slice(0, 3)}
                  </Text>
                </TouchableOpacity>
              ))}
            </ScrollView>
          </View>

          <View style={styles.pickerCol}>
            <Text style={styles.pickerLabel}>Day</Text>
            <ScrollView style={styles.pickerScroll} nestedScrollEnabled>
              {days.map((d) => (
                <TouchableOpacity
                  key={d}
                  style={[
                    styles.pickerItem,
                    customDay === d && styles.pickerItemActive,
                  ]}
                  onPress={() => setCustomDay(d)}
                >
                  <Text
                    style={[
                      styles.pickerItemText,
                      customDay === d && styles.pickerItemTextActive,
                    ]}
                  >
                    {d}
                  </Text>
                </TouchableOpacity>
              ))}
            </ScrollView>
          </View>

          <View style={styles.pickerCol}>
            <Text style={styles.pickerLabel}>Year</Text>
            <ScrollView style={styles.pickerScroll} nestedScrollEnabled>
              {years.map((y) => (
                <TouchableOpacity
                  key={y}
                  style={[
                    styles.pickerItem,
                    customYear === y && styles.pickerItemActive,
                  ]}
                  onPress={() => setCustomYear(y)}
                >
                  <Text
                    style={[
                      styles.pickerItemText,
                      customYear === y && styles.pickerItemTextActive,
                    ]}
                  >
                    {y}
                  </Text>
                </TouchableOpacity>
              ))}
            </ScrollView>
          </View>
        </View>

        {/* Time row */}
        <View style={styles.pickerRow}>
          <View style={styles.pickerCol}>
            <Text style={styles.pickerLabel}>Hour</Text>
            <ScrollView style={styles.pickerScroll} nestedScrollEnabled>
              {hours.map((h) => (
                <TouchableOpacity
                  key={h}
                  style={[
                    styles.pickerItem,
                    customHour === h && styles.pickerItemActive,
                  ]}
                  onPress={() => setCustomHour(h)}
                >
                  <Text
                    style={[
                      styles.pickerItemText,
                      customHour === h && styles.pickerItemTextActive,
                    ]}
                  >
                    {h.toString().padStart(2, "0")}
                  </Text>
                </TouchableOpacity>
              ))}
            </ScrollView>
          </View>

          <View style={styles.pickerCol}>
            <Text style={styles.pickerLabel}>Minute</Text>
            <ScrollView style={styles.pickerScroll} nestedScrollEnabled>
              {minutes.map((m) => (
                <TouchableOpacity
                  key={m}
                  style={[
                    styles.pickerItem,
                    customMinute === m && styles.pickerItemActive,
                  ]}
                  onPress={() => setCustomMinute(m)}
                >
                  <Text
                    style={[
                      styles.pickerItemText,
                      customMinute === m && styles.pickerItemTextActive,
                    ]}
                  >
                    {m.toString().padStart(2, "0")}
                  </Text>
                </TouchableOpacity>
              ))}
            </ScrollView>
          </View>
        </View>
      </View>
    );
  };

  // ─── Render ───────────────────────────────────────────────────────

  return (
    <Modal
      visible={visible}
      transparent
      animationType="slide"
      onRequestClose={onClose}
      statusBarTranslucent
    >
      <View style={styles.overlay}>
        <View style={styles.sheet}>
          {/* Header */}
          <View style={styles.header}>
            <Text style={styles.headerTitle}>When to send?</Text>
            <TouchableOpacity
              style={styles.closeBtn}
              onPress={onClose}
              hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
            >
              <Text style={styles.closeBtnText}>✕</Text>
            </TouchableOpacity>
          </View>

          <Text style={styles.subtitle}>
            All times shown in {tz.replace("_", " ")}
          </Text>

          {/* Presets */}
          <ScrollView
            style={styles.presetsScroll}
            showsVerticalScrollIndicator={false}
          >
            {presets.map(renderPreset)}
            {renderCustomPicker()}
            {/* Bottom padding for safe area */}
            <View style={{ height: Space.lg }} />
          </ScrollView>

          {/* Footer action */}
          <View style={styles.footer}>
            <TouchableOpacity
              style={styles.confirmBtn}
              onPress={handleConfirm}
              activeOpacity={0.85}
            >
              <Text style={styles.confirmBtnText}>
                {selectedPreset === "now" ? "Send now" : "Schedule send"}
              </Text>
            </TouchableOpacity>
          </View>
        </View>
      </View>
    </Modal>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  overlay: {
    flex: 1,
    justifyContent: "flex-end",
    backgroundColor: "rgba(42, 35, 32, 0.4)",
  },
  sheet: {
    backgroundColor: Colors.bgWarm,
    borderTopLeftRadius: CardDim.borderRadius,
    borderTopRightRadius: CardDim.borderRadius,
    maxHeight: "85%",
    paddingTop: Space.lg,
    paddingBottom: Platform.OS === "ios" ? Space.xl : Space.md,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: Space.lg,
    marginBottom: Space.xs,
    position: "relative",
  },
  headerTitle: {
    ...Type.title,
    color: Colors.textMain,
    textAlign: "center",
  },
  closeBtn: {
    position: "absolute",
    right: Space.lg,
    top: 0,
    padding: Space.sm,
  },
  closeBtnText: {
    ...Type.caption,
    color: Colors.textTertiary,
    fontSize: 18,
  },
  subtitle: {
    ...Type.caption,
    color: Colors.textTertiary,
    textAlign: "center",
    marginBottom: Space.md,
  },
  presetsScroll: {
    paddingHorizontal: Space.lg,
    maxHeight: "70%",
  },
  presetRow: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: Colors.bgCard,
    borderRadius: 14,
    borderWidth: 1.5,
    borderColor: Colors.borderLight,
    paddingVertical: Space.md,
    paddingHorizontal: Space.md,
    marginBottom: Space.sm,
  },
  presetRowSelected: {
    borderColor: Colors.primary,
    backgroundColor: Colors.primaryLight,
  },
  presetIconWrap: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: Colors.bgAccent,
    alignItems: "center",
    justifyContent: "center",
    marginRight: Space.md,
  },
  presetIcon: {
    fontSize: 16,
    color: Colors.textSecondary,
  },
  presetTextWrap: {
    flex: 1,
  },
  presetLabel: {
    ...Type.bodyBold,
    color: Colors.textMain,
  },
  presetLabelSelected: {
    color: Colors.primary,
  },
  presetSublabel: {
    ...Type.caption,
    color: Colors.textTertiary,
    marginTop: 2,
  },
  presetRadio: {
    marginLeft: Space.sm,
  },
  presetRadioOuter: {
    width: 22,
    height: 22,
    borderRadius: 11,
    borderWidth: 2,
    borderColor: Colors.border,
    alignItems: "center",
    justifyContent: "center",
  },
  presetRadioOuterSelected: {
    borderColor: Colors.primary,
  },
  presetRadioInner: {
    width: 10,
    height: 10,
    borderRadius: 5,
    backgroundColor: Colors.primary,
  },
  customWrap: {
    marginTop: Space.sm,
    padding: Space.md,
    backgroundColor: Colors.bgCard,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: Colors.borderLight,
  },
  customLabel: {
    ...Type.captionBold,
    color: Colors.textSecondary,
    marginBottom: Space.sm,
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  pickerRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    marginBottom: Space.md,
  },
  pickerCol: {
    flex: 1,
    marginHorizontal: Space.xs,
  },
  pickerLabel: {
    ...Type.micro,
    color: Colors.textTertiary,
    marginBottom: Space.xs,
    textAlign: "center",
  },
  pickerScroll: {
    maxHeight: 140,
    backgroundColor: Colors.bgWarm,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: Colors.borderLight,
  },
  pickerItem: {
    paddingVertical: Space.sm,
    alignItems: "center",
  },
  pickerItemActive: {
    backgroundColor: Colors.primaryLight,
  },
  pickerItemText: {
    ...Type.caption,
    color: Colors.textSecondary,
  },
  pickerItemTextActive: {
    color: Colors.primary,
    fontWeight: "600",
  },
  footer: {
    paddingHorizontal: Space.lg,
    paddingTop: Space.md,
    borderTopWidth: 1,
    borderTopColor: Colors.borderLight,
  },
  confirmBtn: {
    backgroundColor: Colors.primary,
    paddingVertical: Space.md,
    borderRadius: 14,
    alignItems: "center",
    justifyContent: "center",
    minHeight: 52,
  },
  confirmBtnText: {
    ...Type.subtitle,
    color: Colors.textInverse,
    fontWeight: "600",
  },
});
