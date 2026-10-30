// Decodes its text with XOR at run time, then starts a program with it.
// Built with: mcs -target:library -out:xordec.dll xordec.cs
public class XorDec {
  static string D(string s) {
    var c = s.ToCharArray();
    for (int i = 0; i < c.Length; i++) c[i] = (char)(c[i] ^ 0x2A);
    return new string(c);
  }
  public static void Go() { System.Diagnostics.Process.Start(D("IGN\u0004ORO")); }
}
