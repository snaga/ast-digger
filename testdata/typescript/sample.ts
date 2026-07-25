/**
 * Doubles x.
 */
export function topLevelFunction(x: number): number {
  return x * 2;
}

/**
 * A friendly greeter.
 */
export class Greeter {
  name: string;

  /**
   * Store the name.
   */
  constructor(name: string) {
    this.name = name;
  }

  /**
   * Shout a message.
   */
  static shout(message: string): string {
    return message.toUpperCase();
  }
}

/**
 * Describes something that can be greeted.
 */
export interface Greeting {
  message: string;

  /**
   * Format the greeting.
   */
  format(): string;
}

/**
 * A greeting identifier: either a string or a numeric code.
 */
export type GreetingId = string | number;

/**
 * An arrow function assigned to a const.
 */
export const arrowGreet = (name: string): string => {
  return `Hello, ${name}!`;
};

function decoratorFactory() {
  return function (target: unknown): void {
    // no-op decorator used only to exercise decorator parsing
  };
}

/**
 * A class with a decorator, to exercise decorator + JSDoc handling together.
 */
@decoratorFactory()
export class DecoratedThing {
  @decoratorFactory()
  decoratedMethod(): void {
    console.log("decorated");
  }
}

function noDocstringFunction(): void {
  return;
}
