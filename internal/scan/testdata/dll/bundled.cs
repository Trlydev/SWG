// A DLL that carries process.dll inside, deflate-compressed in its
// resource "payload.bin" (how DLL bundlers pack libraries; a loader would
// unpack it and pass it to Assembly.Load). Built with:
//   python3 -c "import zlib,sys;c=zlib.compressobj(9,zlib.DEFLATED,-15);sys.stdout.buffer.write(c.compress(open('process.dll','rb').read())+c.flush())" > payload.bin
//   mcs -target:library -resource:payload.bin -out:bundled.dll bundled.cs
public class Bundled { public static int Answer() { return 42; } }
