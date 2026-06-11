// Module stubs for native Expo/RN packages unavailable in a Vite web build.
// All exports typed as `any` to prevent TS errors in scaffold code.

// Compatibility shim: AsyncStorage v1 methods (multiGet/multiRemove/setItem)
// The v2 package exports an interface without these; augment the default export.
declare module '@react-native-async-storage/async-storage' {
  const AsyncStorage: {
    getItem(key: string): Promise<string | null>
    setItem(key: string, value: string): Promise<void>
    removeItem(key: string): Promise<void>
    multiGet(keys: string[]): Promise<[string, string | null][]>
    multiSet(pairs: [string, string][]): Promise<void>
    multiRemove(keys: string[]): Promise<void>
    clear(): Promise<void>
    getAllKeys(): Promise<string[]>
    getMany(keys: string[]): Promise<Record<string, string | null>>
    setMany(entries: Record<string, string>): Promise<void>
    removeMany(keys: string[]): Promise<void>
  }
  export default AsyncStorage
}

declare module 'expo-background-fetch' {
  export enum BackgroundFetchResult { NewData = 1, NoData = 2, Failed = 3 }
  export enum BackgroundFetchStatus { Denied = 1, Restricted, Available }
  export function registerTaskAsync(taskName: string, options?: object): Promise<void>
  export function unregisterTaskAsync(taskName: string): Promise<void>
  export function isAvailableAsync(): Promise<boolean>
  export function getStatusAsync(): Promise<BackgroundFetchStatus>
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const _: any; export default _
}

declare module 'expo-task-manager' {
  export function defineTask(taskName: string, callback: (args: object) => void): void
  export function isTaskRegisteredAsync(taskName: string): Promise<boolean>
}

declare module 'react-native-aes-crypto' {
  const AesCrypto: {
    pbkdf2(password: string, salt: string, cost: number, length: number, algorithm: string): Promise<string>
    encrypt(text: string, key: string, iv: string, algorithm: string): Promise<string>
    decrypt(ciphertext: string, key: string, iv: string, algorithm: string): Promise<string>
    randomKey(length: number): Promise<string>
    sha256(text: string): Promise<string>
  }
  export default AesCrypto
}

declare module 'expo-secure-store' {
  export const WHEN_UNLOCKED: string
  export const WHEN_UNLOCKED_THIS_DEVICE_ONLY: string
  export const ALWAYS: string
  export function getItemAsync(key: string, options?: object): Promise<string | null>
  export function setItemAsync(key: string, value: string, options?: object): Promise<void>
  export function deleteItemAsync(key: string, options?: object): Promise<void>
}

declare module 'op-sqlite' {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  export type RowSet = Array<any> & { item(i: number): any; length: number }
  export interface DB {
    execute(sql: string, params?: unknown[]): { rows: RowSet }
    executeAsync(sql: string, params?: unknown[]): Promise<{ rows: RowSet }>
    executeBatch(queries: Array<[string, unknown[]?]>): void
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    transaction(fn: (tx: any) => void): void
    close(): void
  }
  export type SQLCmdNames = string
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  export function open(options: any): DB
}

declare module 'expo-notifications' {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  type AnyObj = Record<string, any>
  export type NotificationTriggerInput = AnyObj
  export type Notification = AnyObj
  export type Subscription = { remove(): void }
  export function getPermissionsAsync(): Promise<{ status: string }>
  export function requestPermissionsAsync(options?: AnyObj): Promise<{ status: string }>
  export function getExpoPushTokenAsync(options?: AnyObj): Promise<{ data: string }>
  export function setNotificationHandler(handler: AnyObj): void
  export function setNotificationCategoryAsync(id: string, actions: AnyObj[]): Promise<void>
  export function scheduleNotificationAsync(request: AnyObj): Promise<string>
  export function cancelScheduledNotificationAsync(id: string): Promise<void>
  export function presentNotificationAsync(request: AnyObj): Promise<void>
  export function setBadgeCountAsync(count: number): Promise<boolean>
  export function dismissAllNotificationsAsync(): Promise<void>
  export function removeNotificationSubscription(subscription: Subscription): void
  export function addNotificationReceivedListener(cb: (n: Notification) => void): Subscription
  export function addNotificationResponseReceivedListener(cb: (r: AnyObj) => void): Subscription
}

declare module 'expo-device' {
  export const isDevice: boolean
}

declare module 'expo-constants' {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const Constants: Record<string, any>
  export default Constants
}

declare module 'react-native-reanimated' {
  import type { ComponentType, ForwardRefExoticComponent, RefAttributes } from 'react'
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  type AnyProps = Record<string, any>
  export function useSharedValue<T>(init: T): { value: T }
  export function useAnimatedStyle(fn: () => AnyProps): AnyProps
  export function useAnimatedProps(fn: () => AnyProps): AnyProps
  export function withTiming(value: number, config?: AnyProps, cb?: (finished?: boolean) => void): number
  export function withSpring(value: number, config?: AnyProps): number
  export function withDelay(delayMs: number, animation: number): number
  export function withRepeat(animation: number, count?: number, reverse?: boolean): number
  export function runOnJS<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => void
  export const Easing: {
    linear: number; ease: number; quad: number; cubic: number
    bezier(x1: number, y1: number, x2: number, y2: number): number
    in(easing: number): number; out(easing: number): number; inOut(easing: number): number
  }
  export function interpolate(value: number, inputRange: number[], outputRange: number[], extrapolation?: string): number
  export enum Extrapolation { CLAMP = 'clamp', EXTEND = 'extend', IDENTITY = 'identity' }
  export const FadeIn: AnyProps
  export const FadeOut: AnyProps
  export const SlideInLeft: AnyProps
  export const SlideOutRight: AnyProps
  export const ZoomIn: AnyProps
  export const ZoomOut: AnyProps
  // Animated namespace
  const Animated: {
    View: ComponentType<AnyProps>
    Text: ComponentType<AnyProps>
    Image: ComponentType<AnyProps>
    ScrollView: ComponentType<AnyProps>
    createAnimatedComponent<T extends ComponentType<AnyProps>>(component: T): T
  }
  export default Animated
}

declare module 'react-native-svg' {
  import type { ComponentType } from 'react'
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  type AnyProps = Record<string, any>
  // Named exports
  export const Circle: ComponentType<AnyProps>
  export const Rect: ComponentType<AnyProps>
  export const Path: ComponentType<AnyProps>
  export const G: ComponentType<AnyProps>
  export const Line: ComponentType<AnyProps>
  export const Polyline: ComponentType<AnyProps>
  export const Text: ComponentType<AnyProps>
  export const Defs: ComponentType<AnyProps>
  export const LinearGradient: ComponentType<AnyProps>
  export const Stop: ComponentType<AnyProps>
  export const Ellipse: ComponentType<AnyProps>
  export const Polygon: ComponentType<AnyProps>
  export const ClipPath: ComponentType<AnyProps>
  export const Mask: ComponentType<AnyProps>
  export const Use: ComponentType<AnyProps>
  export const Symbol: ComponentType<AnyProps>
  // Default export is the Svg component itself
  const Svg: ComponentType<AnyProps>
  export default Svg
}
