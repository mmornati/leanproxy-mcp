import * as path from 'path';
import * as fs from 'fs';

export async function run(): Promise<void> {
  const Mocha = (await import('mocha')).default;

  const mocha = new Mocha({
    ui: 'tdd',
    color: true,
    timeout: 10000,
  });

  const testsRoot = path.resolve(__dirname, '..');
  const files = fs.readdirSync(testsRoot)
    .filter((f: string) => f.endsWith('.test.js'))
    .map((f: string) => path.resolve(testsRoot, f));

  for (const f of files) {
    mocha.addFile(f);
  }

  return new Promise<void>((resolve, reject) => {
    try {
      mocha.run((failures: number) => {
        if (failures > 0) {
          reject(new Error(`${failures} tests failed`));
        } else {
          resolve();
        }
      });
    } catch (err) {
      reject(err);
    }
  });
}
