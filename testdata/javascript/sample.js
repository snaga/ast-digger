/**
 * Doubles x.
 */
export function topLevelFunction(x) {
  return x * 2;
}

/**
 * A friendly greeter.
 */
export class Greeter {
  /**
   * Store the name.
   */
  constructor(name) {
    this.name = name;
  }

  /**
   * Shout a message.
   */
  static shout(message) {
    return message.toUpperCase();
  }
}

/**
 * An arrow function assigned to a const.
 */
export const arrowGreet = (name) => {
  return `Hello, ${name}!`;
};

function noDocstringFunction() {
  return;
}
