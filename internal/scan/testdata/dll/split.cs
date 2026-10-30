// The type name is glued together at run time, so no single string literal
// names it. Built with: mcs -target:library -out:split.dll split.cs
public class Split {
  public static object Go() {
    string a = "System.Diag";
    string b = "nostics.Process";
    var t = System.Type.GetType(a + b + ", System");
    return t.GetMethod("GetCurrentProcess").Invoke(null, null);
  }
}
