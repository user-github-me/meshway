using System;
using System.Collections.Generic;
using System.Linq;
using System.Net;
using System.Net.Sockets;
using System.Numerics;

namespace Meshway.VPN
{
    // Compatible with the Windows PowerShell 5.1 Add-Type compiler as well as
    // the native helper. Return disjoint CIDRs excluding exact control scopes.
    public static class FirewallAddressRanges
    {
        private sealed class Span { public BigInteger First; public BigInteger Last; }
        public static string[] Complement(string[] allowed, bool ipv6)
        {
            int bits = ipv6 ? 128 : 32;
            var spans = new List<Span>();
            foreach (string value in allowed)
            {
                string[] fields = value.Split('/');
                IPAddress ip = IPAddress.Parse(fields[0]);
                if ((ip.AddressFamily == AddressFamily.InterNetworkV6) != ipv6) continue;
                int prefix = fields.Length == 1 ? bits : int.Parse(fields[1]);
                if (prefix < 0 || prefix > bits || fields.Length > 2) throw new FormatException("Invalid firewall control prefix");
                BigInteger size = BigInteger.Pow(2, bits - prefix);
                BigInteger first = Number(ip) / size * size;
                spans.Add(new Span { First = first, Last = first + size - 1 });
            }
            var result = new List<string>();
            BigInteger cursor = BigInteger.Zero;
            BigInteger maximum = BigInteger.Pow(2, bits) - 1;
            foreach (Span span in spans.OrderBy(x => x.First))
            {
                if (span.First > cursor) AppendPrefixes(result, cursor, span.First - 1, bits);
                cursor = BigInteger.Max(cursor, span.Last + 1);
            }
            if (cursor <= maximum) AppendPrefixes(result, cursor, maximum, bits);
            return result.ToArray();
        }
        private static void AppendPrefixes(List<string> result, BigInteger first, BigInteger last, int bits)
        {
            while (first <= last)
            {
                int prefix = bits;
                BigInteger size = BigInteger.One;
                while (prefix > 0)
                {
                    BigInteger nextSize = size * 2;
                    if (first % nextSize != 0 || first + nextSize - 1 > last) break;
                    size = nextSize;
                    prefix--;
                }
                result.Add(Address(first, bits) + "/" + prefix);
                first += size;
            }
        }
        private static BigInteger Number(IPAddress ip)
        {
            byte[] bytes = ip.GetAddressBytes();
            Array.Reverse(bytes);
            Array.Resize(ref bytes, bytes.Length + 1);
            return new BigInteger(bytes);
        }
        private static string Address(BigInteger number, int bits)
        {
            byte[] source = number.ToByteArray();
            byte[] bytes = new byte[bits / 8];
            Array.Copy(source, bytes, Math.Min(source.Length, bytes.Length));
            Array.Reverse(bytes);
            return new IPAddress(bytes).ToString();
        }
    }
}
