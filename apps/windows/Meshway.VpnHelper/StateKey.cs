using System.IO;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Cryptography;

namespace Meshway.Security;

public static class StateKey
{
    [StructLayout(LayoutKind.Sequential)] private struct Blob { public int Size; public IntPtr Data; }
    [DllImport("crypt32.dll", SetLastError = true)] private static extern bool CryptProtectData(ref Blob input, string description, IntPtr entropy, IntPtr reserved, IntPtr prompt, uint flags, out Blob output);
    [DllImport("crypt32.dll", SetLastError = true)] private static extern bool CryptUnprotectData(ref Blob input, IntPtr description, IntPtr entropy, IntPtr reserved, IntPtr prompt, uint flags, out Blob output);
    [DllImport("kernel32.dll")] private static extern IntPtr LocalFree(IntPtr memory);

    public static byte[] LoadOrCreate(string configPath)
    {
        var path = Path.Combine(Path.GetDirectoryName(Path.GetFullPath(configPath))!, "state-key.dpapi");
        if (File.Exists(path))
        {
            var key = Transform(File.ReadAllBytes(path), false);
            if (key.Length != 32) throw new CryptographicException("Invalid private storage key.");
            return key;
        }
        var generated = RandomNumberGenerator.GetBytes(32);
        var encoded = Transform(generated, true);
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        using var file = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None);
        file.Write(encoded); file.Flush(true);
        return generated;
    }

    private static byte[] Transform(byte[] bytes, bool protect)
    {
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException("Meshway native storage protection requires Windows DPAPI.");
        var input = new Blob { Size = bytes.Length, Data = Marshal.AllocHGlobal(bytes.Length) };
        try
        {
            Marshal.Copy(bytes, 0, input.Data, bytes.Length);
            Blob output;
            var result = protect ? CryptProtectData(ref input, "Meshway private storage key", IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, 1, out output)
                : CryptUnprotectData(ref input, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, 1, out output);
            if (!result) throw new Win32Exception(Marshal.GetLastWin32Error(), "Windows could not protect or unlock Meshway private state.");
            try { var data = new byte[output.Size]; Marshal.Copy(output.Data, data, 0, data.Length); return data; }
            finally { LocalFree(output.Data); }
        }
        finally { Marshal.FreeHGlobal(input.Data); }
    }
}
