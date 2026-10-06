github.com/go-pdfkit/conformance/compare.draw STEXT size=1592 align=0x0 args=0x20 locals=0x148 funcid=0x0
	0x0000 00000 (/src/compare/compare.go:218)	TEXT	github.com/go-pdfkit/conformance/compare.draw(SB), ABIInternal, $328-32
	0x0000 00000 (/src/compare/compare.go:218)	MOVV	16(g), R20
	0x0004 00004 (/src/compare/compare.go:218)	PCDATA	$0, $-2
	0x0004 00004 (/src/compare/compare.go:218)	ADDV	$-208, R3, R24
	0x0008 00008 (/src/compare/compare.go:218)	SGTU	R24, R20, R20
	0x000c 00012 (/src/compare/compare.go:218)	BEQ	R20, 1548
	0x0010 00016 (/src/compare/compare.go:218)	PCDATA	$0, $-1
	0x0010 00016 (/src/compare/compare.go:218)	PCDATA	$0, $-2
	0x0010 00016 (/src/compare/compare.go:218)	MOVV	R1, -336(R3)
	0x0014 00020 (/src/compare/compare.go:218)	ADDV	$-336, R3
	0x0018 00024 (/src/compare/compare.go:218)	PCDATA	$0, $-1
	0x0018 00024 (/src/compare/compare.go:218)	MOVV	R1, (R3)
	0x001c 00028 (/src/compare/compare.go:218)	MOVV	R0, 320(R3)
	0x0020 00032 (/src/compare/compare.go:218)	MOVV	R0, 328(R3)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$0, gclocals·Nt0hF6NeDl6H4rGxYo2byg==(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$1, gclocals·asxAV7YIv6wkVXbf/qbXzg==(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$2, github.com/go-pdfkit/conformance/compare.draw.stkobj(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$5, github.com/go-pdfkit/conformance/compare.draw.arginfo1(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$6, github.com/go-pdfkit/conformance/compare.draw.argliveinfo(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$4, github.com/go-pdfkit/conformance/compare.draw.opendefer(SB)
	0x0024 00036 (/src/compare/compare.go:218)	PCDATA	$3, $1
	0x0024 00036 (/src/compare/compare.go:220)	MOVV	R6, github.com/go-pdfkit/conformance/compare.page+16(FP)
	0x0028 00040 (/src/compare/compare.go:220)	MOVD	F0, github.com/go-pdfkit/conformance/compare.dpi+24(FP)
	0x002c 00044 (/src/compare/compare.go:220)	MOVV	R5, github.com/go-pdfkit/conformance/compare.path+8(FP)
	0x0030 00048 (/src/compare/compare.go:220)	MOVV	R4, github.com/go-pdfkit/conformance/compare.path(FP)
	0x0034 00052 (/src/compare/compare.go:220)	PCDATA	$3, $-1
	0x0034 00052 (/src/compare/compare.go:218)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0038 00056 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x003c 00060 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0040 00064 (/src/compare/compare.go:218)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0044 00068 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0048 00072 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x004c 00076 (/src/compare/compare.go:219)	MOVV	R0, R4
	0x0050 00080 (/src/compare/compare.go:219)	MOVV	R0, R5
	0x0054 00084 (/src/compare/compare.go:219)	MOVV	$go:string."compare"(SB), R6
	0x005c 00092 (/src/compare/compare.go:219)	MOVV	$7, R7
	0x0060 00096 (/src/compare/compare.go:219)	PCDATA	$1, $1
	0x0060 00096 (/src/compare/compare.go:219)	CALL	os.MkdirTemp(SB)
	0x0064 00100 (/src/compare/compare.go:220)	BNE	R6, 1456
	0x0068 00104 (/src/compare/compare.go:223)	MOVV	$github.com/go-pdfkit/conformance/compare.draw.deferwrap1(SB), R7
	0x0070 00112 (/src/compare/compare.go:223)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_60-120(SP)
	0x0074 00116 (/src/compare/compare.go:223)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_60-104(SP)
	0x0078 00120 (/src/compare/compare.go:223)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_60-112(SP)
	0x007c 00124 (/src/compare/compare.go:223)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_60-120(SP), R7
	0x0080 00128 (/src/compare/compare.go:223)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP)
	0x0084 00132 (/src/compare/compare.go:223)	MOVV	$1, R7
	0x0088 00136 (/src/compare/compare.go:223)	MOVB	R7, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x008c 00140 (/src/compare/compare.go:224)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_62-144(SP)
	0x0090 00144 (/src/compare/compare.go:224)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_62-152(SP)
	0x0094 00148 (/src/compare/compare.go:224)	MOVV	$1, R7
	0x0098 00152 (/src/compare/compare.go:224)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_62-128(SP)
	0x009c 00156 (/src/compare/compare.go:224)	MOVV	$go:string."p"(SB), R7
	0x00a4 00164 (/src/compare/compare.go:224)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_62-136(SP)
	0x00a8 00168 (/src/compare/compare.go:226)	MOVD	github.com/go-pdfkit/conformance/compare.dpi+24(FP), F0
	0x00ac 00172 (/src/compare/compare.go:226)	TRUNCDV	F0, F0
	0x00b0 00176 (/src/compare/compare.go:226)	MOVD	F0, github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP)
	0x00b4 00180 (<unknown line number>)	NOP
	0x00b4 00180 (/src/filepath/path.go:131)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_62-152(SP), R4
	0x00b8 00184 (/src/filepath/path.go:131)	MOVV	$2, R5
	0x00bc 00188 (/src/filepath/path.go:131)	MOVV	R5, R6
	0x00c0 00192 (/src/filepath/path.go:131)	CALL	path/filepath.join(SB)
	0x00c4 00196 (/src/filepath/path.go:131)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP)
	0x00c8 00200 (/src/filepath/path.go:131)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r0.len-272(SP)
	0x00cc 00204 (/src/compare/compare.go:225)	PCDATA	$1, $2
	0x00cc 00204 (/src/compare/compare.go:225)	CALL	time.Now(SB)
	0x00d0 00208 (/src/compare/compare.go:225)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_140-224(SP)
	0x00d4 00212 (/src/compare/compare.go:225)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_141-232(SP)
	0x00d8 00216 (/src/compare/compare.go:225)	MOVV	R6, github.com/go-pdfkit/conformance/compare..autotmp_142-24(SP)
	0x00dc 00220 (/src/compare/compare.go:226)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_47-64(SP)
	0x00e0 00224 (/src/compare/compare.go:226)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_47-56(SP)
	0x00e4 00228 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP), R4
	0x00e8 00232 (/src/compare/compare.go:226)	PCDATA	$1, $3
	0x00e8 00232 (/src/compare/compare.go:226)	CALL	runtime.convT64(SB)
	0x00ec 00236 (/src/compare/compare.go:226)	MOVV	$type:int(SB), R7
	0x00f4 00244 (/src/compare/compare.go:226)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_47-64(SP)
	0x00f8 00248 (/src/compare/compare.go:226)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_47-56(SP)
	0x00fc 00252 (/src/compare/compare.go:226)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_47-64(SP), R4
	0x0100 00256 (/src/compare/compare.go:226)	MOVV	$1, R5
	0x0104 00260 (/src/compare/compare.go:226)	MOVV	R5, R6
	0x0108 00264 (/src/compare/compare.go:226)	PCDATA	$1, $4
	0x0108 00264 (/src/compare/compare.go:226)	CALL	fmt.Sprint(SB)
	0x010c 00268 (/src/compare/compare.go:226)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP)
	0x0110 00272 (/src/compare/compare.go:226)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_143-32(SP)
	0x0114 00276 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_48-80(SP)
	0x0118 00280 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_48-72(SP)
	0x011c 00284 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.page+16(FP), R4
	0x0120 00288 (/src/compare/compare.go:227)	PCDATA	$1, $5
	0x0120 00288 (/src/compare/compare.go:227)	CALL	runtime.convT64(SB)
	0x0124 00292 (/src/compare/compare.go:227)	MOVV	$type:int(SB), R7
	0x012c 00300 (/src/compare/compare.go:227)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_48-80(SP)
	0x0130 00304 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_48-72(SP)
	0x0134 00308 (/src/compare/compare.go:227)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_48-80(SP), R4
	0x0138 00312 (/src/compare/compare.go:227)	MOVV	$1, R5
	0x013c 00316 (/src/compare/compare.go:227)	MOVV	R5, R6
	0x0140 00320 (/src/compare/compare.go:227)	PCDATA	$1, $6
	0x0140 00320 (/src/compare/compare.go:227)	CALL	fmt.Sprint(SB)
	0x0144 00324 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_144-40(SP)
	0x0148 00328 (/src/compare/compare.go:227)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_145-240(SP)
	0x014c 00332 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_50-96(SP)
	0x0150 00336 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_50-88(SP)
	0x0154 00340 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.page+16(FP), R4
	0x0158 00344 (/src/compare/compare.go:227)	PCDATA	$1, $7
	0x0158 00344 (/src/compare/compare.go:227)	CALL	runtime.convT64(SB)
	0x015c 00348 (/src/compare/compare.go:227)	MOVV	$type:int(SB), R7
	0x0164 00356 (/src/compare/compare.go:227)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_50-96(SP)
	0x0168 00360 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_50-88(SP)
	0x016c 00364 (/src/compare/compare.go:227)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_50-96(SP), R4
	0x0170 00368 (/src/compare/compare.go:227)	MOVV	$1, R5
	0x0174 00372 (/src/compare/compare.go:227)	MOVV	R5, R6
	0x0178 00376 (/src/compare/compare.go:227)	PCDATA	$1, $8
	0x0178 00376 (/src/compare/compare.go:227)	CALL	fmt.Sprint(SB)
	0x017c 00380 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_146-48(SP)
	0x0180 00384 (/src/compare/compare.go:227)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_147-248(SP)
	0x0184 00388 (/src/compare/compare.go:226)	MOVV	$type:[10]string(SB), R4
	0x018c 00396 (/src/compare/compare.go:226)	PCDATA	$1, $9
	0x018c 00396 (/src/compare/compare.go:226)	CALL	runtime.newobject(SB)
	0x0190 00400 (/src/compare/compare.go:226)	MOVV	$8, R7
	0x0194 00404 (/src/compare/compare.go:226)	MOVV	R7, 8(R4)
	0x0198 00408 (/src/compare/compare.go:226)	MOVV	$go:string."-cropbox"(SB), R7
	0x01a0 00416 (/src/compare/compare.go:226)	MOVV	R7, (R4)
	0x01a4 00420 (/src/compare/compare.go:226)	MOVV	$2, R7
	0x01a8 00424 (/src/compare/compare.go:226)	MOVV	R7, 24(R4)
	0x01ac 00428 (/src/compare/compare.go:226)	MOVV	$go:string."-r"(SB), R8
	0x01b4 00436 (/src/compare/compare.go:226)	MOVV	R8, 16(R4)
	0x01b8 00440 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP), R8
	0x01bc 00444 (/src/compare/compare.go:226)	MOVV	R8, 40(R4)
	0x01c0 00448 (/src/compare/compare.go:226)	PCDATA	$0, $-3
	0x01c0 00448 (/src/compare/compare.go:226)	MOVWU	runtime.writeBarrier(SB), R8
	0x01c8 00456 (/src/compare/compare.go:226)	PCDATA	$0, $-1
	0x01c8 00456 (/src/compare/compare.go:226)	PCDATA	$0, $-2
	0x01c8 00456 (/src/compare/compare.go:226)	BNE	R8, 468
	0x01cc 00460 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_143-32(SP), R8
	0x01d0 00464 (/src/compare/compare.go:226)	JMP	480
	0x01d4 00468 (/src/compare/compare.go:226)	CALL	runtime.gcWriteBarrier1(SB)
	0x01d8 00472 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_143-32(SP), R8
	0x01dc 00476 (/src/compare/compare.go:226)	MOVV	R8, (R29)
	0x01e0 00480 (/src/compare/compare.go:226)	MOVV	R8, 32(R4)
	0x01e4 00484 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x01e4 00484 (/src/compare/compare.go:227)	MOVV	R7, 56(R4)
	0x01e8 00488 (/src/compare/compare.go:227)	MOVV	$go:string."-f"(SB), R8
	0x01f0 00496 (/src/compare/compare.go:227)	MOVV	R8, 48(R4)
	0x01f4 00500 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_145-240(SP), R8
	0x01f8 00504 (/src/compare/compare.go:227)	MOVV	R8, 72(R4)
	0x01fc 00508 (/src/compare/compare.go:227)	PCDATA	$0, $-4
	0x01fc 00508 (/src/compare/compare.go:227)	MOVWU	runtime.writeBarrier(SB), R8
	0x0204 00516 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0204 00516 (/src/compare/compare.go:227)	PCDATA	$0, $-2
	0x0204 00516 (/src/compare/compare.go:227)	BNE	R8, 528
	0x0208 00520 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_144-40(SP), R8
	0x020c 00524 (/src/compare/compare.go:227)	JMP	540
	0x0210 00528 (/src/compare/compare.go:227)	CALL	runtime.gcWriteBarrier1(SB)
	0x0214 00532 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_144-40(SP), R8
	0x0218 00536 (/src/compare/compare.go:227)	MOVV	R8, (R29)
	0x021c 00540 (/src/compare/compare.go:227)	MOVV	R8, 64(R4)
	0x0220 00544 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0220 00544 (/src/compare/compare.go:227)	MOVV	R7, 88(R4)
	0x0224 00548 (/src/compare/compare.go:227)	MOVV	$go:string."-l"(SB), R7
	0x022c 00556 (/src/compare/compare.go:227)	MOVV	R7, 80(R4)
	0x0230 00560 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_147-248(SP), R7
	0x0234 00564 (/src/compare/compare.go:227)	MOVV	R7, 104(R4)
	0x0238 00568 (/src/compare/compare.go:227)	PCDATA	$0, $-3
	0x0238 00568 (/src/compare/compare.go:227)	MOVWU	runtime.writeBarrier(SB), R7
	0x0240 00576 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0240 00576 (/src/compare/compare.go:227)	PCDATA	$0, $-2
	0x0240 00576 (/src/compare/compare.go:227)	BNE	R7, 588
	0x0244 00580 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_146-48(SP), R7
	0x0248 00584 (/src/compare/compare.go:227)	JMP	600
	0x024c 00588 (/src/compare/compare.go:227)	CALL	runtime.gcWriteBarrier1(SB)
	0x0250 00592 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_146-48(SP), R7
	0x0254 00596 (/src/compare/compare.go:227)	MOVV	R7, (R29)
	0x0258 00600 (/src/compare/compare.go:227)	MOVV	R7, 96(R4)
	0x025c 00604 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x025c 00604 (/src/compare/compare.go:227)	MOVV	$4, R7
	0x0260 00608 (/src/compare/compare.go:227)	MOVV	R7, 120(R4)
	0x0264 00612 (/src/compare/compare.go:227)	MOVV	$go:string."-png"(SB), R7
	0x026c 00620 (/src/compare/compare.go:227)	MOVV	R7, 112(R4)
	0x0270 00624 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.path+8(FP), R7
	0x0274 00628 (/src/compare/compare.go:227)	MOVV	R7, 136(R4)
	0x0278 00632 (/src/compare/compare.go:227)	PCDATA	$0, $-4
	0x0278 00632 (/src/compare/compare.go:227)	MOVWU	runtime.writeBarrier(SB), R7
	0x0280 00640 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0280 00640 (/src/compare/compare.go:227)	PCDATA	$0, $-2
	0x0280 00640 (/src/compare/compare.go:227)	BNE	R7, 656
	0x0284 00644 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.path(FP), R7
	0x0288 00648 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP), R8
	0x028c 00652 (/src/compare/compare.go:227)	JMP	676
	0x0290 00656 (/src/compare/compare.go:227)	CALL	runtime.gcWriteBarrier2(SB)
	0x0294 00660 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.path(FP), R7
	0x0298 00664 (/src/compare/compare.go:227)	MOVV	R7, (R29)
	0x029c 00668 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP), R8
	0x02a0 00672 (/src/compare/compare.go:227)	MOVV	R8, 8(R29)
	0x02a4 00676 (/src/compare/compare.go:227)	MOVV	R7, 128(R4)
	0x02a8 00680 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.len-272(SP), R7
	0x02ac 00684 (/src/compare/compare.go:227)	MOVV	R7, 152(R4)
	0x02b0 00688 (/src/compare/compare.go:227)	MOVV	R8, 144(R4)
	0x02b4 00692 (/src/compare/compare.go:226)	PCDATA	$0, $-1
	0x02b4 00692 (/src/compare/compare.go:226)	PCDATA	$0, $-3
	0x02b4 00692 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare.popplerCommand(SB), R29
	0x02bc 00700 (/src/compare/compare.go:226)	PCDATA	$0, $-1
	0x02bc 00700 (/src/compare/compare.go:226)	MOVV	(R29), R7
	0x02c0 00704 (/src/compare/compare.go:226)	MOVV	$10, R5
	0x02c4 00708 (/src/compare/compare.go:226)	MOVV	R5, R6
	0x02c8 00712 (/src/compare/compare.go:226)	PCDATA	$1, $10
	0x02c8 00712 (/src/compare/compare.go:226)	CALL	(R7)
	0x02cc 00716 (/src/compare/compare.go:226)	MOVB	R4, github.com/go-pdfkit/conformance/compare.hung-282(SP)
	0x02d0 00720 (/src/compare/compare.go:226)	MOVV	R5, github.com/go-pdfkit/conformance/compare.err.itab-256(SP)
	0x02d4 00724 (/src/compare/compare.go:226)	MOVV	R6, github.com/go-pdfkit/conformance/compare.err.data-176(SP)
	0x02d8 00728 (/src/compare/compare.go:228)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_140-224(SP), R4
	0x02dc 00732 (/src/compare/compare.go:228)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_141-232(SP), R5
	0x02e0 00736 (/src/compare/compare.go:228)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_142-24(SP), R6
	0x02e4 00740 (/src/compare/compare.go:228)	PCDATA	$1, $11
	0x02e4 00740 (/src/compare/compare.go:228)	CALL	time.Since(SB)
	0x02e8 00744 (/src/compare/compare.go:226)	MOVBU	github.com/go-pdfkit/conformance/compare.hung-282(SP), R7
	0x02ec 00748 (/src/compare/compare.go:229)	BNE	R7, 1376
	0x02f0 00752 (/src/compare/compare.go:232)	MOVV	github.com/go-pdfkit/conformance/compare.err.itab-256(SP), R5
	0x02f4 00756 (/src/compare/compare.go:232)	BNE	R5, 1304
	0x02f8 00760 (/src/compare/compare.go:228)	MOVV	R4, github.com/go-pdfkit/conformance/compare.took-264(SP)
	0x02fc 00764 (/src/compare/compare.go:235)	MOVV	R0, R4
	0x0300 00768 (/src/compare/compare.go:235)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP), R5
	0x0304 00772 (/src/compare/compare.go:235)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.len-272(SP), R6
	0x0308 00776 (/src/compare/compare.go:235)	MOVV	$go:string."*.png"(SB), R7
	0x0310 00784 (/src/compare/compare.go:235)	MOVV	$5, R8
	0x0314 00788 (/src/compare/compare.go:235)	PCDATA	$1, $12
	0x0314 00788 (/src/compare/compare.go:235)	CALL	runtime.concatstring2(SB)
	0x0318 00792 (<unknown line number>)	NOP
	0x0318 00792 (/src/filepath/match.go:232)	MOVV	R0, R6
	0x031c 00796 (/src/filepath/match.go:232)	CALL	path/filepath.globWithLimit(SB)
	0x0320 00800 (/src/compare/compare.go:236)	BNE	R5, 964
	0x0324 00804 (<unknown line number>)	NOP
	0x0324 00804 (/src/fmt/errors.go:26)	MOVV	$go:string."it wrote no picture"(SB), R4
	0x032c 00812 (/src/fmt/errors.go:26)	MOVV	$19, R5
	0x0330 00816 (/src/fmt/errors.go:26)	MOVV	R0, R6
	0x0334 00820 (/src/fmt/errors.go:26)	MOVV	R0, R7
	0x0338 00824 (/src/fmt/errors.go:26)	MOVV	R0, R8
	0x033c 00828 (/src/fmt/errors.go:26)	CALL	fmt.errorf(SB)
	0x0340 00832 (/src/fmt/errors.go:26)	BNE	R4, 892
	0x0344 00836 (/src/fmt/errors.go:31)	NOOP
	0x0348 00840 (/src/errors/errors.go:65)	MOVV	$16, R4
	0x034c 00844 (/src/errors/errors.go:65)	MOVV	$type:errors.errorString(SB), R5
	0x0354 00852 (/src/errors/errors.go:65)	MOVV	$1, R6
	0x0358 00856 (/src/errors/errors.go:65)	CALL	runtime.mallocgcSmallScanNoHeaderSC2(SB)
	0x035c 00860 (/src/errors/errors.go:65)	MOVV	$19, R7
	0x0360 00864 (/src/errors/errors.go:65)	MOVV	R7, 8(R4)
	0x0364 00868 (/src/errors/errors.go:65)	MOVV	$go:string."it wrote no picture"(SB), R7
	0x036c 00876 (/src/errors/errors.go:65)	MOVV	R7, (R4)
	0x0370 00880 (/src/compare/compare.go:237)	MOVV	R4, R5
	0x0374 00884 (/src/compare/compare.go:237)	MOVV	$go:itab.*errors.errorString,error(SB), R4
	0x037c 00892 (/src/compare/compare.go:237)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0380 00896 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R6
	0x0384 00900 (/src/compare/compare.go:237)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0388 00904 (/src/compare/compare.go:237)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x038c 00908 (/src/compare/compare.go:237)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0390 00912 (/src/compare/compare.go:237)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x0394 00916 (/src/compare/compare.go:237)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0398 00920 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x039c 00924 (/src/compare/compare.go:237)	MOVV	(R29), R4
	0x03a0 00928 (/src/compare/compare.go:237)	CALL	(R4)
	0x03a4 00932 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x03a8 00936 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x03ac 00940 (/src/compare/compare.go:237)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x03b0 00944 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x03b4 00948 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x03b8 00952 (/src/compare/compare.go:237)	MOVV	(R3), R1
	0x03bc 00956 (/src/compare/compare.go:237)	ADDV	$336, R3
	0x03c0 00960 (/src/compare/compare.go:237)	JMP	(R1)
	0x03c4 00964 (/src/compare/compare.go:239)	MOVV	(R4), R8
	0x03c8 00968 (/src/compare/compare.go:239)	MOVV	8(R4), R5
	0x03cc 00972 (<unknown line number>)	NOP
	0x03cc 00972 (/src/os/file.go:390)	MOVV	R8, R4
	0x03d0 00976 (/src/os/file.go:390)	MOVV	R0, R6
	0x03d4 00980 (/src/os/file.go:390)	MOVV	R0, R7
	0x03d8 00984 (/src/os/file.go:390)	CALL	os.OpenFile(SB)
	0x03dc 00988 (/src/compare/compare.go:240)	BNE	R5, 1232
	0x03e0 00992 (/src/compare/compare.go:243)	MOVV	$github.com/go-pdfkit/conformance/compare.draw.deferwrap2(SB), R6
	0x03e8 01000 (/src/compare/compare.go:243)	MOVV	R6, github.com/go-pdfkit/conformance/compare..autotmp_88-168(SP)
	0x03ec 01004 (/src/compare/compare.go:243)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_88-160(SP)
	0x03f0 01008 (/src/compare/compare.go:243)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_88-168(SP), R6
	0x03f4 01012 (/src/compare/compare.go:243)	MOVV	R6, github.com/go-pdfkit/conformance/compare..autotmp_97-8(SP)
	0x03f8 01016 (/src/compare/compare.go:243)	MOVV	$3, R6
	0x03fc 01020 (/src/compare/compare.go:243)	MOVB	R6, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0400 01024 (/src/compare/compare.go:244)	MOVV	R4, R5
	0x0404 01028 (/src/compare/compare.go:244)	MOVV	$go:itab.*os.File,io.Reader(SB), R4
	0x040c 01036 (/src/compare/compare.go:244)	CALL	image/png.Decode(SB)
	0x0410 01040 (/src/compare/compare.go:245)	BEQ	R6, 1136
	0x0414 01044 (/src/compare/compare.go:246)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0418 01048 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R4
	0x041c 01052 (/src/compare/compare.go:246)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0420 01056 (/src/compare/compare.go:246)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0424 01060 (/src/compare/compare.go:246)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0428 01064 (/src/compare/compare.go:246)	MOVV	R7, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x042c 01068 (/src/compare/compare.go:246)	MOVV	$1, R4
	0x0430 01072 (/src/compare/compare.go:246)	MOVB	R4, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0434 01076 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_97-8(SP), R29
	0x0438 01080 (/src/compare/compare.go:246)	MOVV	(R29), R4
	0x043c 01084 (/src/compare/compare.go:246)	CALL	(R4)
	0x0440 01088 (/src/compare/compare.go:246)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0444 01092 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x0448 01096 (/src/compare/compare.go:246)	MOVV	(R29), R4
	0x044c 01100 (/src/compare/compare.go:246)	CALL	(R4)
	0x0450 01104 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x0454 01108 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0458 01112 (/src/compare/compare.go:246)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x045c 01116 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x0460 01120 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x0464 01124 (/src/compare/compare.go:246)	MOVV	(R3), R1
	0x0468 01128 (/src/compare/compare.go:246)	ADDV	$336, R3
	0x046c 01132 (/src/compare/compare.go:246)	JMP	(R1)
	0x0470 01136 (/src/compare/compare.go:248)	CALL	github.com/go-gfx/gfx/raster.FromImage(SB)
	0x0474 01140 (/src/compare/compare.go:248)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0478 01144 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R6
	0x047c 01148 (/src/compare/compare.go:248)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0480 01152 (/src/compare/compare.go:248)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0484 01156 (/src/compare/compare.go:248)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0488 01160 (/src/compare/compare.go:248)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x048c 01164 (/src/compare/compare.go:248)	MOVV	$1, R6
	0x0490 01168 (/src/compare/compare.go:248)	MOVB	R6, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0494 01172 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_97-8(SP), R29
	0x0498 01176 (/src/compare/compare.go:248)	MOVV	(R29), R6
	0x049c 01180 (/src/compare/compare.go:248)	CALL	(R6)
	0x04a0 01184 (/src/compare/compare.go:248)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x04a4 01188 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x04a8 01192 (/src/compare/compare.go:248)	MOVV	(R29), R6
	0x04ac 01196 (/src/compare/compare.go:248)	CALL	(R6)
	0x04b0 01200 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x04b4 01204 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x04b8 01208 (/src/compare/compare.go:248)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x04bc 01212 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x04c0 01216 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x04c4 01220 (/src/compare/compare.go:248)	MOVV	(R3), R1
	0x04c8 01224 (/src/compare/compare.go:248)	ADDV	$336, R3
	0x04cc 01228 (/src/compare/compare.go:248)	JMP	(R1)
	0x04d0 01232 (/src/compare/compare.go:241)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x04d4 01236 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R4
	0x04d8 01240 (/src/compare/compare.go:241)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x04dc 01244 (/src/compare/compare.go:241)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x04e0 01248 (/src/compare/compare.go:241)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x04e4 01252 (/src/compare/compare.go:241)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x04e8 01256 (/src/compare/compare.go:241)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x04ec 01260 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x04f0 01264 (/src/compare/compare.go:241)	MOVV	(R29), R4
	0x04f4 01268 (/src/compare/compare.go:241)	CALL	(R4)
	0x04f8 01272 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x04fc 01276 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0500 01280 (/src/compare/compare.go:241)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x0504 01284 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x0508 01288 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x050c 01292 (/src/compare/compare.go:241)	MOVV	(R3), R1
	0x0510 01296 (/src/compare/compare.go:241)	ADDV	$336, R3
	0x0514 01300 (/src/compare/compare.go:241)	JMP	(R1)
	0x0518 01304 (/src/compare/compare.go:233)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x051c 01308 (/src/compare/compare.go:233)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0520 01312 (/src/compare/compare.go:233)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0524 01316 (/src/compare/compare.go:233)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0528 01320 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.err.data-176(SP), R4
	0x052c 01324 (/src/compare/compare.go:233)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x0530 01328 (/src/compare/compare.go:233)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0534 01332 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x0538 01336 (/src/compare/compare.go:233)	MOVV	(R29), R4
	0x053c 01340 (/src/compare/compare.go:233)	CALL	(R4)
	0x0540 01344 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x0544 01348 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0548 01352 (/src/compare/compare.go:233)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x054c 01356 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x0550 01360 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x0554 01364 (/src/compare/compare.go:233)	MOVV	(R3), R1
	0x0558 01368 (/src/compare/compare.go:233)	ADDV	$336, R3
	0x055c 01372 (/src/compare/compare.go:233)	JMP	(R1)
	0x0560 01376 (/src/compare/compare.go:230)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0564 01380 (/src/compare/compare.go:230)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0568 01384 (/src/compare/compare.go:230)	MOVV	$1, R4
	0x056c 01388 (/src/compare/compare.go:230)	MOVB	R4, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0570 01392 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.err.itab-256(SP), R4
	0x0574 01396 (/src/compare/compare.go:230)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0578 01400 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.err.data-176(SP), R4
	0x057c 01404 (/src/compare/compare.go:230)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x0580 01408 (/src/compare/compare.go:230)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0584 01412 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x0588 01416 (/src/compare/compare.go:230)	MOVV	(R29), R4
	0x058c 01420 (/src/compare/compare.go:230)	CALL	(R4)
	0x0590 01424 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x0594 01428 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0598 01432 (/src/compare/compare.go:230)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x059c 01436 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x05a0 01440 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x05a4 01444 (/src/compare/compare.go:230)	MOVV	(R3), R1
	0x05a8 01448 (/src/compare/compare.go:230)	ADDV	$336, R3
	0x05ac 01452 (/src/compare/compare.go:230)	JMP	(R1)
	0x05b0 01456 (/src/compare/compare.go:221)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x05b4 01460 (/src/compare/compare.go:221)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x05b8 01464 (/src/compare/compare.go:221)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x05bc 01468 (/src/compare/compare.go:221)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x05c0 01472 (/src/compare/compare.go:221)	MOVV	R7, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x05c4 01476 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x05c8 01480 (/src/compare/compare.go:221)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R9
	0x05cc 01484 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x05d0 01488 (/src/compare/compare.go:221)	MOVV	R7, R8
	0x05d4 01492 (/src/compare/compare.go:221)	MOVV	R6, R7
	0x05d8 01496 (/src/compare/compare.go:221)	MOVV	R9, R6
	0x05dc 01500 (/src/compare/compare.go:221)	MOVV	(R3), R1
	0x05e0 01504 (/src/compare/compare.go:221)	ADDV	$336, R3
	0x05e4 01508 (/src/compare/compare.go:221)	JMP	(R1)
	0x05e8 01512 (/src/compare/compare.go:221)	CALL	runtime.deferreturn(SB)
	0x05ec 01516 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x05f0 01520 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x05f4 01524 (/src/compare/compare.go:221)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x05f8 01528 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x05fc 01532 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x0600 01536 (/src/compare/compare.go:221)	MOVV	(R3), R1
	0x0604 01540 (/src/compare/compare.go:221)	ADDV	$336, R3
	0x0608 01544 (/src/compare/compare.go:221)	JMP	(R1)
	0x060c 01548 (/src/compare/compare.go:221)	NOP
	0x060c 01548 (/src/compare/compare.go:218)	PCDATA	$1, $-1
	0x060c 01548 (/src/compare/compare.go:218)	PCDATA	$0, $-2
	0x060c 01548 (/src/compare/compare.go:218)	MOVV	R4, 8(R3)
	0x0610 01552 (/src/compare/compare.go:218)	MOVV	R5, 16(R3)
	0x0614 01556 (/src/compare/compare.go:218)	MOVV	R6, 24(R3)
	0x0618 01560 (/src/compare/compare.go:218)	MOVD	F0, 32(R3)
	0x061c 01564 (/src/compare/compare.go:218)	MOVV	R1, R31
	0x0620 01568 (/src/compare/compare.go:218)	CALL	runtime.morestack_noctxt(SB)
	0x0624 01572 (/src/compare/compare.go:218)	PCDATA	$0, $-1
	0x0624 01572 (/src/compare/compare.go:218)	MOVV	8(R3), R4
	0x0628 01576 (/src/compare/compare.go:218)	MOVV	16(R3), R5
	0x062c 01580 (/src/compare/compare.go:218)	MOVV	24(R3), R6
	0x0630 01584 (/src/compare/compare.go:218)	MOVD	32(R3), F0
	0x0634 01588 (/src/compare/compare.go:218)	JMP	0
	0x0000 d4 42 c0 28 78 c0 fc 02 94 e2 12 00 80 02 06 40  .B.(x..........@
	0x0010 61 c0 fa 29 63 c0 fa 02 61 00 c0 29 60 00 c5 29  a..)c...a..)`..)
	0x0020 60 20 c5 29 66 a0 c5 29 60 c0 c5 2b 65 80 c5 29  ` .)f..)`..+e..)
	0x0030 64 60 c5 29 60 dc 00 29 60 60 c2 29 60 e0 c0 29  d`.)`..)``.)`..)
	0x0040 60 d4 00 29 60 00 c2 29 60 20 c2 29 04 00 15 00  `..)`..)` .)....
	0x0050 05 00 15 00 06 00 00 1a c6 00 c0 02 07 1c 80 03  ................
	0x0060 00 00 00 54 c0 4c 05 44 07 00 00 1a e7 00 c0 02  ...T.L.D........
	0x0070 67 60 c3 29 65 a0 c3 29 64 80 c3 29 67 60 c3 02  g`.)e..)d..)g`..
	0x0080 67 00 c5 29 07 04 80 03 67 dc 00 29 65 00 c3 29  g..)....g..)e..)
	0x0090 64 e0 c2 29 07 04 80 03 67 40 c3 29 07 00 00 1a  d..)....g@.)....
	0x00a0 e7 00 c0 02 67 20 c3 29 60 c0 85 2b 00 a8 1a 01  ....g .)`..+....
	0x00b0 60 e0 c1 2b 64 e0 c2 02 05 08 80 03 a6 00 15 00  `..+d...........
	0x00c0 00 00 00 54 64 40 c2 29 65 00 c1 29 00 00 00 54  ...Td@.)e..)...T
	0x00d0 64 c0 c1 29 65 a0 c1 29 66 e0 c4 29 60 40 c4 29  d..)e..)f..)`@.)
	0x00e0 60 60 c4 29 64 e0 c1 28 00 00 00 54 07 00 00 1a  ``.)d..(...T....
	0x00f0 e7 00 c0 02 67 40 c4 29 64 60 c4 29 64 40 c4 02  ....g@.)d`.)d@..
	0x0100 05 04 80 03 a6 00 15 00 00 00 00 54 65 e0 c1 29  ...........Te..)
	0x0110 64 c0 c4 29 60 00 c4 29 60 20 c4 29 64 a0 c5 28  d..)`..)` .)d..(
	0x0120 00 00 00 54 07 00 00 1a e7 00 c0 02 67 00 c4 29  ...T........g..)
	0x0130 64 20 c4 29 64 00 c4 02 05 04 80 03 a6 00 15 00  d .)d...........
	0x0140 00 00 00 54 64 a0 c4 29 65 80 c1 29 60 c0 c3 29  ...Td..)e..)`..)
	0x0150 60 e0 c3 29 64 a0 c5 28 00 00 00 54 07 00 00 1a  `..)d..(...T....
	0x0160 e7 00 c0 02 67 c0 c3 29 64 e0 c3 29 64 c0 c3 02  ....g..)d..)d...
	0x0170 05 04 80 03 a6 00 15 00 00 00 00 54 64 80 c4 29  ...........Td..)
	0x0180 65 60 c1 29 04 00 00 1a 84 00 c0 02 00 00 00 54  e`.)...........T
	0x0190 07 20 80 03 87 20 c0 29 07 00 00 1a e7 00 c0 02  . ... .)........
	0x01a0 87 00 c0 29 07 08 80 03 87 60 c0 29 08 00 00 1a  ...).....`.)....
	0x01b0 08 01 c0 02 88 40 c0 29 68 e0 c1 28 88 a0 c0 29  .....@.)h..(...)
	0x01c0 1e 00 00 1a c8 03 80 2a 00 0d 00 44 68 c0 c4 28  .......*...Dh..(
	0x01d0 00 10 00 50 00 00 00 54 68 c0 c4 28 a8 03 c0 29  ...P...Th..(...)
	0x01e0 88 80 c0 29 87 e0 c0 29 08 00 00 1a 08 01 c0 02  ...)...)........
	0x01f0 88 c0 c0 29 68 80 c1 28 88 20 c1 29 1e 00 00 1a  ...)h..(. .)....
	0x0200 c8 03 80 2a 00 0d 00 44 68 a0 c4 28 00 10 00 50  ...*...Dh..(...P
	0x0210 00 00 00 54 68 a0 c4 28 a8 03 c0 29 88 00 c1 29  ...Th..(...)...)
	0x0220 87 60 c1 29 07 00 00 1a e7 00 c0 02 87 40 c1 29  .`.).........@.)
	0x0230 67 60 c1 28 87 a0 c1 29 1e 00 00 1a c7 03 80 2a  g`.(...).......*
	0x0240 e0 0c 00 44 67 80 c4 28 00 10 00 50 00 00 00 54  ...Dg..(...P...T
	0x0250 67 80 c4 28 a7 03 c0 29 87 80 c1 29 07 10 80 03  g..(...)...)....
	0x0260 87 e0 c1 29 07 00 00 1a e7 00 c0 02 87 c0 c1 29  ...)...........)
	0x0270 67 80 c5 28 87 20 c2 29 1e 00 00 1a c7 03 80 2a  g..(. .).......*
	0x0280 e0 10 00 44 67 60 c5 28 68 40 c2 28 00 18 00 50  ...Dg`.(h@.(...P
	0x0290 00 00 00 54 67 60 c5 28 a7 03 c0 29 68 40 c2 28  ...Tg`.(...)h@.(
	0x02a0 a8 23 c0 29 87 00 c2 29 67 00 c1 28 87 60 c2 29  .#.)...)g..(.`.)
	0x02b0 88 40 c2 29 1e 00 00 1a dd 03 c0 28 a7 03 c0 28  .@.).......(...(
	0x02c0 05 28 80 03 a6 00 15 00 e1 00 00 4c 64 d8 00 29  .(.........Ld..)
	0x02d0 65 40 c1 29 66 80 c2 29 64 c0 c1 28 65 a0 c1 28  e@.)f..)d..(e..(
	0x02e0 66 e0 c4 28 00 00 00 54 67 d8 00 2a e0 74 02 44  f..(...Tg..*.t.D
	0x02f0 65 40 c1 28 a0 24 02 44 64 20 c1 29 04 00 15 00  e@.(.$.Dd .)....
	0x0300 65 40 c2 28 66 00 c1 28 07 00 00 1a e7 00 c0 02  e@.(f..(........
	0x0310 08 14 80 03 00 00 00 54 06 00 15 00 00 00 00 54  .......T.......T
	0x0320 a0 a4 00 44 04 00 00 1a 84 00 c0 02 05 4c 80 03  ...D.........L..
	0x0330 06 00 15 00 07 00 15 00 08 00 15 00 00 00 00 54  ...............T
	0x0340 80 3c 00 44 00 00 40 03 04 40 80 03 05 00 00 1a  .<.D..@..@......
	0x0350 a5 00 c0 02 06 04 80 03 00 00 00 54 07 4c 80 03  ...........T.L..
	0x0360 87 20 c0 29 07 00 00 1a e7 00 c0 02 87 00 c0 29  . .)...........)
	0x0370 85 00 15 00 04 00 00 1a 84 00 c0 02 60 60 c2 29  ............``.)
	0x0380 66 20 c1 28 66 e0 c0 29 60 d4 00 29 64 00 c2 29  f .(f..)`..)d..)
	0x0390 65 20 c2 29 60 dc 00 29 7d 00 c5 28 a4 03 c0 28  e .)`..)}..(...(
	0x03a0 81 00 00 4c 65 e0 c0 28 67 00 c2 28 66 d4 00 2a  ...Le..(g..(f..*
	0x03b0 64 60 c2 28 68 20 c2 28 61 00 c0 28 63 40 c5 02  d`.(h .(a..(c@..
	0x03c0 20 00 00 4c 88 00 c0 28 85 20 c0 28 04 01 15 00   ..L...(. .(....
	0x03d0 06 00 15 00 07 00 15 00 00 00 00 54 a0 f4 00 44  ...........T...D
	0x03e0 06 00 00 1a c6 00 c0 02 66 a0 c2 29 64 c0 c2 29  ........f..)d..)
	0x03f0 66 a0 c2 02 66 20 c5 29 06 0c 80 03 66 dc 00 29  f...f .)....f..)
	0x0400 85 00 15 00 04 00 00 1a 84 00 c0 02 00 00 00 54  ...............T
	0x0410 c0 60 00 40 60 60 c2 29 64 20 c1 28 64 e0 c0 29  .`.@``.)d .(d..)
	0x0420 60 d4 00 29 66 00 c2 29 67 20 c2 29 04 04 80 03  `..)f..)g .)....
	0x0430 64 dc 00 29 7d 20 c5 28 a4 03 c0 28 81 00 00 4c  d..)} .(...(...L
	0x0440 60 dc 00 29 7d 00 c5 28 a4 03 c0 28 81 00 00 4c  `..)}..(...(...L
	0x0450 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x0460 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x0470 00 00 00 54 64 60 c2 29 66 20 c1 28 66 e0 c0 29  ...Td`.)f .(f..)
	0x0480 60 d4 00 29 60 00 c2 29 60 20 c2 29 06 04 80 03  `..)`..)` .)....
	0x0490 66 dc 00 29 7d 20 c5 28 a6 03 c0 28 c1 00 00 4c  f..)} .(...(...L
	0x04a0 60 dc 00 29 7d 00 c5 28 a6 03 c0 28 c1 00 00 4c  `..)}..(...(...L
	0x04b0 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x04c0 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x04d0 60 60 c2 29 64 20 c1 28 64 e0 c0 29 60 d4 00 29  ``.)d .(d..)`..)
	0x04e0 65 00 c2 29 66 20 c2 29 60 dc 00 29 7d 00 c5 28  e..)f .)`..)}..(
	0x04f0 a4 03 c0 28 81 00 00 4c 65 e0 c0 28 67 00 c2 28  ...(...Le..(g..(
	0x0500 66 d4 00 2a 64 60 c2 28 68 20 c2 28 61 00 c0 28  f..*d`.(h .(a..(
	0x0510 63 40 c5 02 20 00 00 4c 60 60 c2 29 64 e0 c0 29  c@.. ..L``.)d..)
	0x0520 60 d4 00 29 65 00 c2 29 64 80 c2 28 64 20 c2 29  `..)e..)d..(d .)
	0x0530 60 dc 00 29 7d 00 c5 28 a4 03 c0 28 81 00 00 4c  `..)}..(...(...L
	0x0540 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x0550 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x0560 60 60 c2 29 64 e0 c0 29 04 04 80 03 64 d4 00 29  ``.)d..)....d..)
	0x0570 64 40 c1 28 64 00 c2 29 64 80 c2 28 64 20 c2 29  d@.(d..)d..(d .)
	0x0580 60 dc 00 29 7d 00 c5 28 a4 03 c0 28 81 00 00 4c  `..)}..(...(...L
	0x0590 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x05a0 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x05b0 60 60 c2 29 60 e0 c0 29 60 d4 00 29 66 00 c2 29  ``.)`..)`..)f..)
	0x05c0 67 20 c2 29 65 e0 c0 28 69 d4 00 2a 64 60 c2 28  g .)e..(i..*d`.(
	0x05d0 e8 00 15 00 c7 00 15 00 26 01 15 00 61 00 c0 28  ........&...a..(
	0x05e0 63 40 c5 02 20 00 00 4c 00 00 00 54 64 60 c2 28  c@.. ..L...Td`.(
	0x05f0 65 e0 c0 28 66 d4 00 2a 67 00 c2 28 68 20 c2 28  e..(f..*g..(h .(
	0x0600 61 00 c0 28 63 40 c5 02 20 00 00 4c 64 20 c0 29  a..(c@.. ..Ld .)
	0x0610 65 40 c0 29 66 60 c0 29 60 80 c0 2b 3f 00 15 00  e@.)f`.)`..+?...
	0x0620 00 00 00 54 64 20 c0 28 65 40 c0 28 66 60 c0 28  ...Td .(e@.(f`.(
	0x0630 60 80 80 2b ff cf f9 53                          `..+...S
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:*errors.errorString+0
	rel 0+0 t=R_USEIFACE type:*os.File+0
	rel 84+4 t=R_LOONG64_ADDR_HI go:string."compare"+0
	rel 88+4 t=R_LOONG64_ADDR_LO go:string."compare"+0
	rel 96+4 t=R_CALLLOONG64 os.MkdirTemp+0
	rel 104+4 t=R_LOONG64_ADDR_HI github.com/go-pdfkit/conformance/compare.draw.deferwrap1+0
	rel 108+4 t=R_LOONG64_ADDR_LO github.com/go-pdfkit/conformance/compare.draw.deferwrap1+0
	rel 156+4 t=R_LOONG64_ADDR_HI go:string."p"+0
	rel 160+4 t=R_LOONG64_ADDR_LO go:string."p"+0
	rel 192+4 t=R_CALLLOONG64 path/filepath.join+0
	rel 204+4 t=R_CALLLOONG64 time.Now+0
	rel 232+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 236+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 240+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 264+4 t=R_CALLLOONG64 fmt.Sprint+0
	rel 288+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 292+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 296+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 320+4 t=R_CALLLOONG64 fmt.Sprint+0
	rel 344+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 348+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 352+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 376+4 t=R_CALLLOONG64 fmt.Sprint+0
	rel 388+4 t=R_LOONG64_ADDR_HI type:[10]string+0
	rel 392+4 t=R_LOONG64_ADDR_LO type:[10]string+0
	rel 396+4 t=R_CALLLOONG64 runtime.newobject+0
	rel 408+4 t=R_LOONG64_ADDR_HI go:string."-cropbox"+0
	rel 412+4 t=R_LOONG64_ADDR_LO go:string."-cropbox"+0
	rel 428+4 t=R_LOONG64_ADDR_HI go:string."-r"+0
	rel 432+4 t=R_LOONG64_ADDR_LO go:string."-r"+0
	rel 448+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 452+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 468+4 t=R_CALLLOONG64 runtime.gcWriteBarrier1+0
	rel 488+4 t=R_LOONG64_ADDR_HI go:string."-f"+0
	rel 492+4 t=R_LOONG64_ADDR_LO go:string."-f"+0
	rel 508+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 512+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 528+4 t=R_CALLLOONG64 runtime.gcWriteBarrier1+0
	rel 548+4 t=R_LOONG64_ADDR_HI go:string."-l"+0
	rel 552+4 t=R_LOONG64_ADDR_LO go:string."-l"+0
	rel 568+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 572+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 588+4 t=R_CALLLOONG64 runtime.gcWriteBarrier1+0
	rel 612+4 t=R_LOONG64_ADDR_HI go:string."-png"+0
	rel 616+4 t=R_LOONG64_ADDR_LO go:string."-png"+0
	rel 632+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 636+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 656+4 t=R_CALLLOONG64 runtime.gcWriteBarrier2+0
	rel 692+4 t=R_LOONG64_ADDR_HI github.com/go-pdfkit/conformance/compare.popplerCommand+0
	rel 696+4 t=R_LOONG64_ADDR_LO github.com/go-pdfkit/conformance/compare.popplerCommand+0
	rel 712+0 t=R_CALLIND +0
	rel 740+4 t=R_CALLLOONG64 time.Since+0
	rel 776+4 t=R_LOONG64_ADDR_HI go:string."*.png"+0
	rel 780+4 t=R_LOONG64_ADDR_LO go:string."*.png"+0
	rel 788+4 t=R_CALLLOONG64 runtime.concatstring2+0
	rel 796+4 t=R_CALLLOONG64 path/filepath.globWithLimit+0
	rel 804+4 t=R_LOONG64_ADDR_HI go:string."it wrote no picture"+0
	rel 808+4 t=R_LOONG64_ADDR_LO go:string."it wrote no picture"+0
	rel 828+4 t=R_CALLLOONG64 fmt.errorf+0
	rel 844+4 t=R_LOONG64_ADDR_HI type:errors.errorString+0
	rel 848+4 t=R_LOONG64_ADDR_LO type:errors.errorString+0
	rel 856+4 t=R_CALLLOONG64 runtime.mallocgcSmallScanNoHeaderSC2+0
	rel 868+4 t=R_LOONG64_ADDR_HI go:string."it wrote no picture"+0
	rel 872+4 t=R_LOONG64_ADDR_LO go:string."it wrote no picture"+0
	rel 884+4 t=R_LOONG64_ADDR_HI go:itab.*errors.errorString,error+0
	rel 888+4 t=R_LOONG64_ADDR_LO go:itab.*errors.errorString,error+0
	rel 928+0 t=R_CALLIND +0
	rel 984+4 t=R_CALLLOONG64 os.OpenFile+0
	rel 992+4 t=R_LOONG64_ADDR_HI github.com/go-pdfkit/conformance/compare.draw.deferwrap2+0
	rel 996+4 t=R_LOONG64_ADDR_LO github.com/go-pdfkit/conformance/compare.draw.deferwrap2+0
	rel 1028+4 t=R_LOONG64_ADDR_HI go:itab.*os.File,io.Reader+0
	rel 1032+4 t=R_LOONG64_ADDR_LO go:itab.*os.File,io.Reader+0
	rel 1036+4 t=R_CALLLOONG64 image/png.Decode+0
	rel 1084+0 t=R_CALLIND +0
	rel 1100+0 t=R_CALLIND +0
	rel 1136+4 t=R_CALLLOONG64 github.com/go-gfx/gfx/raster.FromImage+0
	rel 1180+0 t=R_CALLIND +0
	rel 1196+0 t=R_CALLIND +0
	rel 1268+0 t=R_CALLIND +0
	rel 1340+0 t=R_CALLIND +0
	rel 1420+0 t=R_CALLIND +0
	rel 1512+4 t=R_CALLLOONG64 runtime.deferreturn+0
	rel 1568+4 t=R_CALLLOONG64 runtime.morestack_noctxt+0
github.com/go-pdfkit/conformance/compare.draw STEXT size=1592 align=0x0 args=0x20 locals=0x148 funcid=0x0
	0x0000 00000 (/src/compare/compare.go:218)	TEXT	github.com/go-pdfkit/conformance/compare.draw(SB), ABIInternal, $328-32
	0x0000 00000 (/src/compare/compare.go:218)	MOVV	16(g), R20
	0x0004 00004 (/src/compare/compare.go:218)	PCDATA	$0, $-2
	0x0004 00004 (/src/compare/compare.go:218)	ADDV	$-208, R3, R24
	0x0008 00008 (/src/compare/compare.go:218)	SGTU	R24, R20, R20
	0x000c 00012 (/src/compare/compare.go:218)	BEQ	R20, 1548
	0x0010 00016 (/src/compare/compare.go:218)	PCDATA	$0, $-1
	0x0010 00016 (/src/compare/compare.go:218)	PCDATA	$0, $-2
	0x0010 00016 (/src/compare/compare.go:218)	MOVV	R1, -336(R3)
	0x0014 00020 (/src/compare/compare.go:218)	ADDV	$-336, R3
	0x0018 00024 (/src/compare/compare.go:218)	PCDATA	$0, $-1
	0x0018 00024 (/src/compare/compare.go:218)	MOVV	R1, (R3)
	0x001c 00028 (/src/compare/compare.go:218)	MOVV	R0, 320(R3)
	0x0020 00032 (/src/compare/compare.go:218)	MOVV	R0, 328(R3)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$0, gclocals·Nt0hF6NeDl6H4rGxYo2byg==(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$1, gclocals·asxAV7YIv6wkVXbf/qbXzg==(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$2, github.com/go-pdfkit/conformance/compare.draw.stkobj(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$5, github.com/go-pdfkit/conformance/compare.draw.arginfo1(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$6, github.com/go-pdfkit/conformance/compare.draw.argliveinfo(SB)
	0x0024 00036 (/src/compare/compare.go:218)	FUNCDATA	$4, github.com/go-pdfkit/conformance/compare.draw.opendefer(SB)
	0x0024 00036 (/src/compare/compare.go:218)	PCDATA	$3, $1
	0x0024 00036 (/src/compare/compare.go:220)	MOVV	R6, github.com/go-pdfkit/conformance/compare.page+16(FP)
	0x0028 00040 (/src/compare/compare.go:220)	MOVD	F0, github.com/go-pdfkit/conformance/compare.dpi+24(FP)
	0x002c 00044 (/src/compare/compare.go:220)	MOVV	R5, github.com/go-pdfkit/conformance/compare.path+8(FP)
	0x0030 00048 (/src/compare/compare.go:220)	MOVV	R4, github.com/go-pdfkit/conformance/compare.path(FP)
	0x0034 00052 (/src/compare/compare.go:220)	PCDATA	$3, $-1
	0x0034 00052 (/src/compare/compare.go:218)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0038 00056 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x003c 00060 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0040 00064 (/src/compare/compare.go:218)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0044 00068 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0048 00072 (/src/compare/compare.go:218)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x004c 00076 (/src/compare/compare.go:219)	MOVV	R0, R4
	0x0050 00080 (/src/compare/compare.go:219)	MOVV	R0, R5
	0x0054 00084 (/src/compare/compare.go:219)	MOVV	$go:string."compare"(SB), R6
	0x005c 00092 (/src/compare/compare.go:219)	MOVV	$7, R7
	0x0060 00096 (/src/compare/compare.go:219)	PCDATA	$1, $1
	0x0060 00096 (/src/compare/compare.go:219)	CALL	os.MkdirTemp(SB)
	0x0064 00100 (/src/compare/compare.go:220)	BNE	R6, 1456
	0x0068 00104 (/src/compare/compare.go:223)	MOVV	$github.com/go-pdfkit/conformance/compare.draw.deferwrap1(SB), R7
	0x0070 00112 (/src/compare/compare.go:223)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_60-120(SP)
	0x0074 00116 (/src/compare/compare.go:223)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_60-104(SP)
	0x0078 00120 (/src/compare/compare.go:223)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_60-112(SP)
	0x007c 00124 (/src/compare/compare.go:223)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_60-120(SP), R7
	0x0080 00128 (/src/compare/compare.go:223)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP)
	0x0084 00132 (/src/compare/compare.go:223)	MOVV	$1, R7
	0x0088 00136 (/src/compare/compare.go:223)	MOVB	R7, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x008c 00140 (/src/compare/compare.go:224)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_62-144(SP)
	0x0090 00144 (/src/compare/compare.go:224)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_62-152(SP)
	0x0094 00148 (/src/compare/compare.go:224)	MOVV	$1, R7
	0x0098 00152 (/src/compare/compare.go:224)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_62-128(SP)
	0x009c 00156 (/src/compare/compare.go:224)	MOVV	$go:string."p"(SB), R7
	0x00a4 00164 (/src/compare/compare.go:224)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_62-136(SP)
	0x00a8 00168 (/src/compare/compare.go:226)	MOVD	github.com/go-pdfkit/conformance/compare.dpi+24(FP), F0
	0x00ac 00172 (/src/compare/compare.go:226)	TRUNCDV	F0, F0
	0x00b0 00176 (/src/compare/compare.go:226)	MOVD	F0, github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP)
	0x00b4 00180 (<unknown line number>)	NOP
	0x00b4 00180 (/src/filepath/path.go:131)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_62-152(SP), R4
	0x00b8 00184 (/src/filepath/path.go:131)	MOVV	$2, R5
	0x00bc 00188 (/src/filepath/path.go:131)	MOVV	R5, R6
	0x00c0 00192 (/src/filepath/path.go:131)	CALL	path/filepath.join(SB)
	0x00c4 00196 (/src/filepath/path.go:131)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP)
	0x00c8 00200 (/src/filepath/path.go:131)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r0.len-272(SP)
	0x00cc 00204 (/src/compare/compare.go:225)	PCDATA	$1, $2
	0x00cc 00204 (/src/compare/compare.go:225)	CALL	time.Now(SB)
	0x00d0 00208 (/src/compare/compare.go:225)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_140-224(SP)
	0x00d4 00212 (/src/compare/compare.go:225)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_141-232(SP)
	0x00d8 00216 (/src/compare/compare.go:225)	MOVV	R6, github.com/go-pdfkit/conformance/compare..autotmp_142-24(SP)
	0x00dc 00220 (/src/compare/compare.go:226)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_47-64(SP)
	0x00e0 00224 (/src/compare/compare.go:226)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_47-56(SP)
	0x00e4 00228 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP), R4
	0x00e8 00232 (/src/compare/compare.go:226)	PCDATA	$1, $3
	0x00e8 00232 (/src/compare/compare.go:226)	CALL	runtime.convT64(SB)
	0x00ec 00236 (/src/compare/compare.go:226)	MOVV	$type:int(SB), R7
	0x00f4 00244 (/src/compare/compare.go:226)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_47-64(SP)
	0x00f8 00248 (/src/compare/compare.go:226)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_47-56(SP)
	0x00fc 00252 (/src/compare/compare.go:226)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_47-64(SP), R4
	0x0100 00256 (/src/compare/compare.go:226)	MOVV	$1, R5
	0x0104 00260 (/src/compare/compare.go:226)	MOVV	R5, R6
	0x0108 00264 (/src/compare/compare.go:226)	PCDATA	$1, $4
	0x0108 00264 (/src/compare/compare.go:226)	CALL	fmt.Sprint(SB)
	0x010c 00268 (/src/compare/compare.go:226)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP)
	0x0110 00272 (/src/compare/compare.go:226)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_143-32(SP)
	0x0114 00276 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_48-80(SP)
	0x0118 00280 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_48-72(SP)
	0x011c 00284 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.page+16(FP), R4
	0x0120 00288 (/src/compare/compare.go:227)	PCDATA	$1, $5
	0x0120 00288 (/src/compare/compare.go:227)	CALL	runtime.convT64(SB)
	0x0124 00292 (/src/compare/compare.go:227)	MOVV	$type:int(SB), R7
	0x012c 00300 (/src/compare/compare.go:227)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_48-80(SP)
	0x0130 00304 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_48-72(SP)
	0x0134 00308 (/src/compare/compare.go:227)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_48-80(SP), R4
	0x0138 00312 (/src/compare/compare.go:227)	MOVV	$1, R5
	0x013c 00316 (/src/compare/compare.go:227)	MOVV	R5, R6
	0x0140 00320 (/src/compare/compare.go:227)	PCDATA	$1, $6
	0x0140 00320 (/src/compare/compare.go:227)	CALL	fmt.Sprint(SB)
	0x0144 00324 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_144-40(SP)
	0x0148 00328 (/src/compare/compare.go:227)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_145-240(SP)
	0x014c 00332 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_50-96(SP)
	0x0150 00336 (/src/compare/compare.go:227)	MOVV	R0, github.com/go-pdfkit/conformance/compare..autotmp_50-88(SP)
	0x0154 00340 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.page+16(FP), R4
	0x0158 00344 (/src/compare/compare.go:227)	PCDATA	$1, $7
	0x0158 00344 (/src/compare/compare.go:227)	CALL	runtime.convT64(SB)
	0x015c 00348 (/src/compare/compare.go:227)	MOVV	$type:int(SB), R7
	0x0164 00356 (/src/compare/compare.go:227)	MOVV	R7, github.com/go-pdfkit/conformance/compare..autotmp_50-96(SP)
	0x0168 00360 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_50-88(SP)
	0x016c 00364 (/src/compare/compare.go:227)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_50-96(SP), R4
	0x0170 00368 (/src/compare/compare.go:227)	MOVV	$1, R5
	0x0174 00372 (/src/compare/compare.go:227)	MOVV	R5, R6
	0x0178 00376 (/src/compare/compare.go:227)	PCDATA	$1, $8
	0x0178 00376 (/src/compare/compare.go:227)	CALL	fmt.Sprint(SB)
	0x017c 00380 (/src/compare/compare.go:227)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_146-48(SP)
	0x0180 00384 (/src/compare/compare.go:227)	MOVV	R5, github.com/go-pdfkit/conformance/compare..autotmp_147-248(SP)
	0x0184 00388 (/src/compare/compare.go:226)	MOVV	$type:[10]string(SB), R4
	0x018c 00396 (/src/compare/compare.go:226)	PCDATA	$1, $9
	0x018c 00396 (/src/compare/compare.go:226)	CALL	runtime.newobject(SB)
	0x0190 00400 (/src/compare/compare.go:226)	MOVV	$8, R7
	0x0194 00404 (/src/compare/compare.go:226)	MOVV	R7, 8(R4)
	0x0198 00408 (/src/compare/compare.go:226)	MOVV	$go:string."-cropbox"(SB), R7
	0x01a0 00416 (/src/compare/compare.go:226)	MOVV	R7, (R4)
	0x01a4 00420 (/src/compare/compare.go:226)	MOVV	$2, R7
	0x01a8 00424 (/src/compare/compare.go:226)	MOVV	R7, 24(R4)
	0x01ac 00428 (/src/compare/compare.go:226)	MOVV	$go:string."-r"(SB), R8
	0x01b4 00436 (/src/compare/compare.go:226)	MOVV	R8, 16(R4)
	0x01b8 00440 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_139-216(SP), R8
	0x01bc 00444 (/src/compare/compare.go:226)	MOVV	R8, 40(R4)
	0x01c0 00448 (/src/compare/compare.go:226)	PCDATA	$0, $-3
	0x01c0 00448 (/src/compare/compare.go:226)	MOVWU	runtime.writeBarrier(SB), R8
	0x01c8 00456 (/src/compare/compare.go:226)	PCDATA	$0, $-1
	0x01c8 00456 (/src/compare/compare.go:226)	PCDATA	$0, $-2
	0x01c8 00456 (/src/compare/compare.go:226)	BNE	R8, 468
	0x01cc 00460 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_143-32(SP), R8
	0x01d0 00464 (/src/compare/compare.go:226)	JMP	480
	0x01d4 00468 (/src/compare/compare.go:226)	CALL	runtime.gcWriteBarrier1(SB)
	0x01d8 00472 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_143-32(SP), R8
	0x01dc 00476 (/src/compare/compare.go:226)	MOVV	R8, (R29)
	0x01e0 00480 (/src/compare/compare.go:226)	MOVV	R8, 32(R4)
	0x01e4 00484 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x01e4 00484 (/src/compare/compare.go:227)	MOVV	R7, 56(R4)
	0x01e8 00488 (/src/compare/compare.go:227)	MOVV	$go:string."-f"(SB), R8
	0x01f0 00496 (/src/compare/compare.go:227)	MOVV	R8, 48(R4)
	0x01f4 00500 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_145-240(SP), R8
	0x01f8 00504 (/src/compare/compare.go:227)	MOVV	R8, 72(R4)
	0x01fc 00508 (/src/compare/compare.go:227)	PCDATA	$0, $-4
	0x01fc 00508 (/src/compare/compare.go:227)	MOVWU	runtime.writeBarrier(SB), R8
	0x0204 00516 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0204 00516 (/src/compare/compare.go:227)	PCDATA	$0, $-2
	0x0204 00516 (/src/compare/compare.go:227)	BNE	R8, 528
	0x0208 00520 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_144-40(SP), R8
	0x020c 00524 (/src/compare/compare.go:227)	JMP	540
	0x0210 00528 (/src/compare/compare.go:227)	CALL	runtime.gcWriteBarrier1(SB)
	0x0214 00532 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_144-40(SP), R8
	0x0218 00536 (/src/compare/compare.go:227)	MOVV	R8, (R29)
	0x021c 00540 (/src/compare/compare.go:227)	MOVV	R8, 64(R4)
	0x0220 00544 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0220 00544 (/src/compare/compare.go:227)	MOVV	R7, 88(R4)
	0x0224 00548 (/src/compare/compare.go:227)	MOVV	$go:string."-l"(SB), R7
	0x022c 00556 (/src/compare/compare.go:227)	MOVV	R7, 80(R4)
	0x0230 00560 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_147-248(SP), R7
	0x0234 00564 (/src/compare/compare.go:227)	MOVV	R7, 104(R4)
	0x0238 00568 (/src/compare/compare.go:227)	PCDATA	$0, $-3
	0x0238 00568 (/src/compare/compare.go:227)	MOVWU	runtime.writeBarrier(SB), R7
	0x0240 00576 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0240 00576 (/src/compare/compare.go:227)	PCDATA	$0, $-2
	0x0240 00576 (/src/compare/compare.go:227)	BNE	R7, 588
	0x0244 00580 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_146-48(SP), R7
	0x0248 00584 (/src/compare/compare.go:227)	JMP	600
	0x024c 00588 (/src/compare/compare.go:227)	CALL	runtime.gcWriteBarrier1(SB)
	0x0250 00592 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_146-48(SP), R7
	0x0254 00596 (/src/compare/compare.go:227)	MOVV	R7, (R29)
	0x0258 00600 (/src/compare/compare.go:227)	MOVV	R7, 96(R4)
	0x025c 00604 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x025c 00604 (/src/compare/compare.go:227)	MOVV	$4, R7
	0x0260 00608 (/src/compare/compare.go:227)	MOVV	R7, 120(R4)
	0x0264 00612 (/src/compare/compare.go:227)	MOVV	$go:string."-png"(SB), R7
	0x026c 00620 (/src/compare/compare.go:227)	MOVV	R7, 112(R4)
	0x0270 00624 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.path+8(FP), R7
	0x0274 00628 (/src/compare/compare.go:227)	MOVV	R7, 136(R4)
	0x0278 00632 (/src/compare/compare.go:227)	PCDATA	$0, $-4
	0x0278 00632 (/src/compare/compare.go:227)	MOVWU	runtime.writeBarrier(SB), R7
	0x0280 00640 (/src/compare/compare.go:227)	PCDATA	$0, $-1
	0x0280 00640 (/src/compare/compare.go:227)	PCDATA	$0, $-2
	0x0280 00640 (/src/compare/compare.go:227)	BNE	R7, 656
	0x0284 00644 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.path(FP), R7
	0x0288 00648 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP), R8
	0x028c 00652 (/src/compare/compare.go:227)	JMP	676
	0x0290 00656 (/src/compare/compare.go:227)	CALL	runtime.gcWriteBarrier2(SB)
	0x0294 00660 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.path(FP), R7
	0x0298 00664 (/src/compare/compare.go:227)	MOVV	R7, (R29)
	0x029c 00668 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP), R8
	0x02a0 00672 (/src/compare/compare.go:227)	MOVV	R8, 8(R29)
	0x02a4 00676 (/src/compare/compare.go:227)	MOVV	R7, 128(R4)
	0x02a8 00680 (/src/compare/compare.go:227)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.len-272(SP), R7
	0x02ac 00684 (/src/compare/compare.go:227)	MOVV	R7, 152(R4)
	0x02b0 00688 (/src/compare/compare.go:227)	MOVV	R8, 144(R4)
	0x02b4 00692 (/src/compare/compare.go:226)	PCDATA	$0, $-1
	0x02b4 00692 (/src/compare/compare.go:226)	PCDATA	$0, $-3
	0x02b4 00692 (/src/compare/compare.go:226)	MOVV	github.com/go-pdfkit/conformance/compare.popplerCommand(SB), R29
	0x02bc 00700 (/src/compare/compare.go:226)	PCDATA	$0, $-1
	0x02bc 00700 (/src/compare/compare.go:226)	MOVV	(R29), R7
	0x02c0 00704 (/src/compare/compare.go:226)	MOVV	$10, R5
	0x02c4 00708 (/src/compare/compare.go:226)	MOVV	R5, R6
	0x02c8 00712 (/src/compare/compare.go:226)	PCDATA	$1, $10
	0x02c8 00712 (/src/compare/compare.go:226)	CALL	(R7)
	0x02cc 00716 (/src/compare/compare.go:226)	MOVB	R4, github.com/go-pdfkit/conformance/compare.hung-282(SP)
	0x02d0 00720 (/src/compare/compare.go:226)	MOVV	R5, github.com/go-pdfkit/conformance/compare.err.itab-256(SP)
	0x02d4 00724 (/src/compare/compare.go:226)	MOVV	R6, github.com/go-pdfkit/conformance/compare.err.data-176(SP)
	0x02d8 00728 (/src/compare/compare.go:228)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_140-224(SP), R4
	0x02dc 00732 (/src/compare/compare.go:228)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_141-232(SP), R5
	0x02e0 00736 (/src/compare/compare.go:228)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_142-24(SP), R6
	0x02e4 00740 (/src/compare/compare.go:228)	PCDATA	$1, $11
	0x02e4 00740 (/src/compare/compare.go:228)	CALL	time.Since(SB)
	0x02e8 00744 (/src/compare/compare.go:226)	MOVBU	github.com/go-pdfkit/conformance/compare.hung-282(SP), R7
	0x02ec 00748 (/src/compare/compare.go:229)	BNE	R7, 1376
	0x02f0 00752 (/src/compare/compare.go:232)	MOVV	github.com/go-pdfkit/conformance/compare.err.itab-256(SP), R5
	0x02f4 00756 (/src/compare/compare.go:232)	BNE	R5, 1304
	0x02f8 00760 (/src/compare/compare.go:228)	MOVV	R4, github.com/go-pdfkit/conformance/compare.took-264(SP)
	0x02fc 00764 (/src/compare/compare.go:235)	MOVV	R0, R4
	0x0300 00768 (/src/compare/compare.go:235)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.ptr-192(SP), R5
	0x0304 00772 (/src/compare/compare.go:235)	MOVV	github.com/go-pdfkit/conformance/compare.~r0.len-272(SP), R6
	0x0308 00776 (/src/compare/compare.go:235)	MOVV	$go:string."*.png"(SB), R7
	0x0310 00784 (/src/compare/compare.go:235)	MOVV	$5, R8
	0x0314 00788 (/src/compare/compare.go:235)	PCDATA	$1, $12
	0x0314 00788 (/src/compare/compare.go:235)	CALL	runtime.concatstring2(SB)
	0x0318 00792 (<unknown line number>)	NOP
	0x0318 00792 (/src/filepath/match.go:232)	MOVV	R0, R6
	0x031c 00796 (/src/filepath/match.go:232)	CALL	path/filepath.globWithLimit(SB)
	0x0320 00800 (/src/compare/compare.go:236)	BNE	R5, 964
	0x0324 00804 (<unknown line number>)	NOP
	0x0324 00804 (/src/fmt/errors.go:26)	MOVV	$go:string."it wrote no picture"(SB), R4
	0x032c 00812 (/src/fmt/errors.go:26)	MOVV	$19, R5
	0x0330 00816 (/src/fmt/errors.go:26)	MOVV	R0, R6
	0x0334 00820 (/src/fmt/errors.go:26)	MOVV	R0, R7
	0x0338 00824 (/src/fmt/errors.go:26)	MOVV	R0, R8
	0x033c 00828 (/src/fmt/errors.go:26)	CALL	fmt.errorf(SB)
	0x0340 00832 (/src/fmt/errors.go:26)	BNE	R4, 892
	0x0344 00836 (/src/fmt/errors.go:31)	NOOP
	0x0348 00840 (/src/errors/errors.go:65)	MOVV	$16, R4
	0x034c 00844 (/src/errors/errors.go:65)	MOVV	$type:errors.errorString(SB), R5
	0x0354 00852 (/src/errors/errors.go:65)	MOVV	$1, R6
	0x0358 00856 (/src/errors/errors.go:65)	CALL	runtime.mallocgcSmallScanNoHeaderSC2(SB)
	0x035c 00860 (/src/errors/errors.go:65)	MOVV	$19, R7
	0x0360 00864 (/src/errors/errors.go:65)	MOVV	R7, 8(R4)
	0x0364 00868 (/src/errors/errors.go:65)	MOVV	$go:string."it wrote no picture"(SB), R7
	0x036c 00876 (/src/errors/errors.go:65)	MOVV	R7, (R4)
	0x0370 00880 (/src/compare/compare.go:237)	MOVV	R4, R5
	0x0374 00884 (/src/compare/compare.go:237)	MOVV	$go:itab.*errors.errorString,error(SB), R4
	0x037c 00892 (/src/compare/compare.go:237)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0380 00896 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R6
	0x0384 00900 (/src/compare/compare.go:237)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0388 00904 (/src/compare/compare.go:237)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x038c 00908 (/src/compare/compare.go:237)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0390 00912 (/src/compare/compare.go:237)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x0394 00916 (/src/compare/compare.go:237)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0398 00920 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x039c 00924 (/src/compare/compare.go:237)	MOVV	(R29), R4
	0x03a0 00928 (/src/compare/compare.go:237)	CALL	(R4)
	0x03a4 00932 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x03a8 00936 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x03ac 00940 (/src/compare/compare.go:237)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x03b0 00944 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x03b4 00948 (/src/compare/compare.go:237)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x03b8 00952 (/src/compare/compare.go:237)	MOVV	(R3), R1
	0x03bc 00956 (/src/compare/compare.go:237)	ADDV	$336, R3
	0x03c0 00960 (/src/compare/compare.go:237)	JMP	(R1)
	0x03c4 00964 (/src/compare/compare.go:239)	MOVV	(R4), R8
	0x03c8 00968 (/src/compare/compare.go:239)	MOVV	8(R4), R5
	0x03cc 00972 (<unknown line number>)	NOP
	0x03cc 00972 (/src/os/file.go:390)	MOVV	R8, R4
	0x03d0 00976 (/src/os/file.go:390)	MOVV	R0, R6
	0x03d4 00980 (/src/os/file.go:390)	MOVV	R0, R7
	0x03d8 00984 (/src/os/file.go:390)	CALL	os.OpenFile(SB)
	0x03dc 00988 (/src/compare/compare.go:240)	BNE	R5, 1232
	0x03e0 00992 (/src/compare/compare.go:243)	MOVV	$github.com/go-pdfkit/conformance/compare.draw.deferwrap2(SB), R6
	0x03e8 01000 (/src/compare/compare.go:243)	MOVV	R6, github.com/go-pdfkit/conformance/compare..autotmp_88-168(SP)
	0x03ec 01004 (/src/compare/compare.go:243)	MOVV	R4, github.com/go-pdfkit/conformance/compare..autotmp_88-160(SP)
	0x03f0 01008 (/src/compare/compare.go:243)	MOVV	$github.com/go-pdfkit/conformance/compare..autotmp_88-168(SP), R6
	0x03f4 01012 (/src/compare/compare.go:243)	MOVV	R6, github.com/go-pdfkit/conformance/compare..autotmp_97-8(SP)
	0x03f8 01016 (/src/compare/compare.go:243)	MOVV	$3, R6
	0x03fc 01020 (/src/compare/compare.go:243)	MOVB	R6, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0400 01024 (/src/compare/compare.go:244)	MOVV	R4, R5
	0x0404 01028 (/src/compare/compare.go:244)	MOVV	$go:itab.*os.File,io.Reader(SB), R4
	0x040c 01036 (/src/compare/compare.go:244)	CALL	image/png.Decode(SB)
	0x0410 01040 (/src/compare/compare.go:245)	BEQ	R6, 1136
	0x0414 01044 (/src/compare/compare.go:246)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0418 01048 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R4
	0x041c 01052 (/src/compare/compare.go:246)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0420 01056 (/src/compare/compare.go:246)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0424 01060 (/src/compare/compare.go:246)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0428 01064 (/src/compare/compare.go:246)	MOVV	R7, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x042c 01068 (/src/compare/compare.go:246)	MOVV	$1, R4
	0x0430 01072 (/src/compare/compare.go:246)	MOVB	R4, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0434 01076 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_97-8(SP), R29
	0x0438 01080 (/src/compare/compare.go:246)	MOVV	(R29), R4
	0x043c 01084 (/src/compare/compare.go:246)	CALL	(R4)
	0x0440 01088 (/src/compare/compare.go:246)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0444 01092 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x0448 01096 (/src/compare/compare.go:246)	MOVV	(R29), R4
	0x044c 01100 (/src/compare/compare.go:246)	CALL	(R4)
	0x0450 01104 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x0454 01108 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0458 01112 (/src/compare/compare.go:246)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x045c 01116 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x0460 01120 (/src/compare/compare.go:246)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x0464 01124 (/src/compare/compare.go:246)	MOVV	(R3), R1
	0x0468 01128 (/src/compare/compare.go:246)	ADDV	$336, R3
	0x046c 01132 (/src/compare/compare.go:246)	JMP	(R1)
	0x0470 01136 (/src/compare/compare.go:248)	CALL	github.com/go-gfx/gfx/raster.FromImage(SB)
	0x0474 01140 (/src/compare/compare.go:248)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0478 01144 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R6
	0x047c 01148 (/src/compare/compare.go:248)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0480 01152 (/src/compare/compare.go:248)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0484 01156 (/src/compare/compare.go:248)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0488 01160 (/src/compare/compare.go:248)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x048c 01164 (/src/compare/compare.go:248)	MOVV	$1, R6
	0x0490 01168 (/src/compare/compare.go:248)	MOVB	R6, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0494 01172 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_97-8(SP), R29
	0x0498 01176 (/src/compare/compare.go:248)	MOVV	(R29), R6
	0x049c 01180 (/src/compare/compare.go:248)	CALL	(R6)
	0x04a0 01184 (/src/compare/compare.go:248)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x04a4 01188 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x04a8 01192 (/src/compare/compare.go:248)	MOVV	(R29), R6
	0x04ac 01196 (/src/compare/compare.go:248)	CALL	(R6)
	0x04b0 01200 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x04b4 01204 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x04b8 01208 (/src/compare/compare.go:248)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x04bc 01212 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x04c0 01216 (/src/compare/compare.go:248)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x04c4 01220 (/src/compare/compare.go:248)	MOVV	(R3), R1
	0x04c8 01224 (/src/compare/compare.go:248)	ADDV	$336, R3
	0x04cc 01228 (/src/compare/compare.go:248)	JMP	(R1)
	0x04d0 01232 (/src/compare/compare.go:241)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x04d4 01236 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.took-264(SP), R4
	0x04d8 01240 (/src/compare/compare.go:241)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x04dc 01244 (/src/compare/compare.go:241)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x04e0 01248 (/src/compare/compare.go:241)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x04e4 01252 (/src/compare/compare.go:241)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x04e8 01256 (/src/compare/compare.go:241)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x04ec 01260 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x04f0 01264 (/src/compare/compare.go:241)	MOVV	(R29), R4
	0x04f4 01268 (/src/compare/compare.go:241)	CALL	(R4)
	0x04f8 01272 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x04fc 01276 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0500 01280 (/src/compare/compare.go:241)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x0504 01284 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x0508 01288 (/src/compare/compare.go:241)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x050c 01292 (/src/compare/compare.go:241)	MOVV	(R3), R1
	0x0510 01296 (/src/compare/compare.go:241)	ADDV	$336, R3
	0x0514 01300 (/src/compare/compare.go:241)	JMP	(R1)
	0x0518 01304 (/src/compare/compare.go:233)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x051c 01308 (/src/compare/compare.go:233)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0520 01312 (/src/compare/compare.go:233)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0524 01316 (/src/compare/compare.go:233)	MOVV	R5, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0528 01320 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.err.data-176(SP), R4
	0x052c 01324 (/src/compare/compare.go:233)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x0530 01328 (/src/compare/compare.go:233)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0534 01332 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x0538 01336 (/src/compare/compare.go:233)	MOVV	(R29), R4
	0x053c 01340 (/src/compare/compare.go:233)	CALL	(R4)
	0x0540 01344 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x0544 01348 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0548 01352 (/src/compare/compare.go:233)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x054c 01356 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x0550 01360 (/src/compare/compare.go:233)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x0554 01364 (/src/compare/compare.go:233)	MOVV	(R3), R1
	0x0558 01368 (/src/compare/compare.go:233)	ADDV	$336, R3
	0x055c 01372 (/src/compare/compare.go:233)	JMP	(R1)
	0x0560 01376 (/src/compare/compare.go:230)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x0564 01380 (/src/compare/compare.go:230)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x0568 01384 (/src/compare/compare.go:230)	MOVV	$1, R4
	0x056c 01388 (/src/compare/compare.go:230)	MOVB	R4, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x0570 01392 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.err.itab-256(SP), R4
	0x0574 01396 (/src/compare/compare.go:230)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x0578 01400 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.err.data-176(SP), R4
	0x057c 01404 (/src/compare/compare.go:230)	MOVV	R4, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x0580 01408 (/src/compare/compare.go:230)	MOVB	R0, github.com/go-pdfkit/conformance/compare..autotmp_95-281(SP)
	0x0584 01412 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare..autotmp_96-16(SP), R29
	0x0588 01416 (/src/compare/compare.go:230)	MOVV	(R29), R4
	0x058c 01420 (/src/compare/compare.go:230)	CALL	(R4)
	0x0590 01424 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x0594 01428 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x0598 01432 (/src/compare/compare.go:230)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x059c 01436 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x05a0 01440 (/src/compare/compare.go:230)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x05a4 01444 (/src/compare/compare.go:230)	MOVV	(R3), R1
	0x05a8 01448 (/src/compare/compare.go:230)	ADDV	$336, R3
	0x05ac 01452 (/src/compare/compare.go:230)	JMP	(R1)
	0x05b0 01456 (/src/compare/compare.go:221)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r0-184(SP)
	0x05b4 01460 (/src/compare/compare.go:221)	MOVV	R0, github.com/go-pdfkit/conformance/compare.~r1-280(SP)
	0x05b8 01464 (/src/compare/compare.go:221)	MOVB	R0, github.com/go-pdfkit/conformance/compare.~r2-283(SP)
	0x05bc 01468 (/src/compare/compare.go:221)	MOVV	R6, github.com/go-pdfkit/conformance/compare.~r3-208(SP)
	0x05c0 01472 (/src/compare/compare.go:221)	MOVV	R7, github.com/go-pdfkit/conformance/compare.~r3-200(SP)
	0x05c4 01476 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x05c8 01480 (/src/compare/compare.go:221)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R9
	0x05cc 01484 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x05d0 01488 (/src/compare/compare.go:221)	MOVV	R7, R8
	0x05d4 01492 (/src/compare/compare.go:221)	MOVV	R6, R7
	0x05d8 01496 (/src/compare/compare.go:221)	MOVV	R9, R6
	0x05dc 01500 (/src/compare/compare.go:221)	MOVV	(R3), R1
	0x05e0 01504 (/src/compare/compare.go:221)	ADDV	$336, R3
	0x05e4 01508 (/src/compare/compare.go:221)	JMP	(R1)
	0x05e8 01512 (/src/compare/compare.go:221)	CALL	runtime.deferreturn(SB)
	0x05ec 01516 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r0-184(SP), R4
	0x05f0 01520 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r1-280(SP), R5
	0x05f4 01524 (/src/compare/compare.go:221)	MOVBU	github.com/go-pdfkit/conformance/compare.~r2-283(SP), R6
	0x05f8 01528 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-208(SP), R7
	0x05fc 01532 (/src/compare/compare.go:221)	MOVV	github.com/go-pdfkit/conformance/compare.~r3-200(SP), R8
	0x0600 01536 (/src/compare/compare.go:221)	MOVV	(R3), R1
	0x0604 01540 (/src/compare/compare.go:221)	ADDV	$336, R3
	0x0608 01544 (/src/compare/compare.go:221)	JMP	(R1)
	0x060c 01548 (/src/compare/compare.go:221)	NOP
	0x060c 01548 (/src/compare/compare.go:218)	PCDATA	$1, $-1
	0x060c 01548 (/src/compare/compare.go:218)	PCDATA	$0, $-2
	0x060c 01548 (/src/compare/compare.go:218)	MOVV	R4, 8(R3)
	0x0610 01552 (/src/compare/compare.go:218)	MOVV	R5, 16(R3)
	0x0614 01556 (/src/compare/compare.go:218)	MOVV	R6, 24(R3)
	0x0618 01560 (/src/compare/compare.go:218)	MOVD	F0, 32(R3)
	0x061c 01564 (/src/compare/compare.go:218)	MOVV	R1, R31
	0x0620 01568 (/src/compare/compare.go:218)	CALL	runtime.morestack_noctxt(SB)
	0x0624 01572 (/src/compare/compare.go:218)	PCDATA	$0, $-1
	0x0624 01572 (/src/compare/compare.go:218)	MOVV	8(R3), R4
	0x0628 01576 (/src/compare/compare.go:218)	MOVV	16(R3), R5
	0x062c 01580 (/src/compare/compare.go:218)	MOVV	24(R3), R6
	0x0630 01584 (/src/compare/compare.go:218)	MOVD	32(R3), F0
	0x0634 01588 (/src/compare/compare.go:218)	JMP	0
	0x0000 d4 42 c0 28 78 c0 fc 02 94 e2 12 00 80 02 06 40  .B.(x..........@
	0x0010 61 c0 fa 29 63 c0 fa 02 61 00 c0 29 60 00 c5 29  a..)c...a..)`..)
	0x0020 60 20 c5 29 66 a0 c5 29 60 c0 c5 2b 65 80 c5 29  ` .)f..)`..+e..)
	0x0030 64 60 c5 29 60 dc 00 29 60 60 c2 29 60 e0 c0 29  d`.)`..)``.)`..)
	0x0040 60 d4 00 29 60 00 c2 29 60 20 c2 29 04 00 15 00  `..)`..)` .)....
	0x0050 05 00 15 00 06 00 00 1a c6 00 c0 02 07 1c 80 03  ................
	0x0060 00 00 00 54 c0 4c 05 44 07 00 00 1a e7 00 c0 02  ...T.L.D........
	0x0070 67 60 c3 29 65 a0 c3 29 64 80 c3 29 67 60 c3 02  g`.)e..)d..)g`..
	0x0080 67 00 c5 29 07 04 80 03 67 dc 00 29 65 00 c3 29  g..)....g..)e..)
	0x0090 64 e0 c2 29 07 04 80 03 67 40 c3 29 07 00 00 1a  d..)....g@.)....
	0x00a0 e7 00 c0 02 67 20 c3 29 60 c0 85 2b 00 a8 1a 01  ....g .)`..+....
	0x00b0 60 e0 c1 2b 64 e0 c2 02 05 08 80 03 a6 00 15 00  `..+d...........
	0x00c0 00 00 00 54 64 40 c2 29 65 00 c1 29 00 00 00 54  ...Td@.)e..)...T
	0x00d0 64 c0 c1 29 65 a0 c1 29 66 e0 c4 29 60 40 c4 29  d..)e..)f..)`@.)
	0x00e0 60 60 c4 29 64 e0 c1 28 00 00 00 54 07 00 00 1a  ``.)d..(...T....
	0x00f0 e7 00 c0 02 67 40 c4 29 64 60 c4 29 64 40 c4 02  ....g@.)d`.)d@..
	0x0100 05 04 80 03 a6 00 15 00 00 00 00 54 65 e0 c1 29  ...........Te..)
	0x0110 64 c0 c4 29 60 00 c4 29 60 20 c4 29 64 a0 c5 28  d..)`..)` .)d..(
	0x0120 00 00 00 54 07 00 00 1a e7 00 c0 02 67 00 c4 29  ...T........g..)
	0x0130 64 20 c4 29 64 00 c4 02 05 04 80 03 a6 00 15 00  d .)d...........
	0x0140 00 00 00 54 64 a0 c4 29 65 80 c1 29 60 c0 c3 29  ...Td..)e..)`..)
	0x0150 60 e0 c3 29 64 a0 c5 28 00 00 00 54 07 00 00 1a  `..)d..(...T....
	0x0160 e7 00 c0 02 67 c0 c3 29 64 e0 c3 29 64 c0 c3 02  ....g..)d..)d...
	0x0170 05 04 80 03 a6 00 15 00 00 00 00 54 64 80 c4 29  ...........Td..)
	0x0180 65 60 c1 29 04 00 00 1a 84 00 c0 02 00 00 00 54  e`.)...........T
	0x0190 07 20 80 03 87 20 c0 29 07 00 00 1a e7 00 c0 02  . ... .)........
	0x01a0 87 00 c0 29 07 08 80 03 87 60 c0 29 08 00 00 1a  ...).....`.)....
	0x01b0 08 01 c0 02 88 40 c0 29 68 e0 c1 28 88 a0 c0 29  .....@.)h..(...)
	0x01c0 1e 00 00 1a c8 03 80 2a 00 0d 00 44 68 c0 c4 28  .......*...Dh..(
	0x01d0 00 10 00 50 00 00 00 54 68 c0 c4 28 a8 03 c0 29  ...P...Th..(...)
	0x01e0 88 80 c0 29 87 e0 c0 29 08 00 00 1a 08 01 c0 02  ...)...)........
	0x01f0 88 c0 c0 29 68 80 c1 28 88 20 c1 29 1e 00 00 1a  ...)h..(. .)....
	0x0200 c8 03 80 2a 00 0d 00 44 68 a0 c4 28 00 10 00 50  ...*...Dh..(...P
	0x0210 00 00 00 54 68 a0 c4 28 a8 03 c0 29 88 00 c1 29  ...Th..(...)...)
	0x0220 87 60 c1 29 07 00 00 1a e7 00 c0 02 87 40 c1 29  .`.).........@.)
	0x0230 67 60 c1 28 87 a0 c1 29 1e 00 00 1a c7 03 80 2a  g`.(...).......*
	0x0240 e0 0c 00 44 67 80 c4 28 00 10 00 50 00 00 00 54  ...Dg..(...P...T
	0x0250 67 80 c4 28 a7 03 c0 29 87 80 c1 29 07 10 80 03  g..(...)...)....
	0x0260 87 e0 c1 29 07 00 00 1a e7 00 c0 02 87 c0 c1 29  ...)...........)
	0x0270 67 80 c5 28 87 20 c2 29 1e 00 00 1a c7 03 80 2a  g..(. .).......*
	0x0280 e0 10 00 44 67 60 c5 28 68 40 c2 28 00 18 00 50  ...Dg`.(h@.(...P
	0x0290 00 00 00 54 67 60 c5 28 a7 03 c0 29 68 40 c2 28  ...Tg`.(...)h@.(
	0x02a0 a8 23 c0 29 87 00 c2 29 67 00 c1 28 87 60 c2 29  .#.)...)g..(.`.)
	0x02b0 88 40 c2 29 1e 00 00 1a dd 03 c0 28 a7 03 c0 28  .@.).......(...(
	0x02c0 05 28 80 03 a6 00 15 00 e1 00 00 4c 64 d8 00 29  .(.........Ld..)
	0x02d0 65 40 c1 29 66 80 c2 29 64 c0 c1 28 65 a0 c1 28  e@.)f..)d..(e..(
	0x02e0 66 e0 c4 28 00 00 00 54 67 d8 00 2a e0 74 02 44  f..(...Tg..*.t.D
	0x02f0 65 40 c1 28 a0 24 02 44 64 20 c1 29 04 00 15 00  e@.(.$.Dd .)....
	0x0300 65 40 c2 28 66 00 c1 28 07 00 00 1a e7 00 c0 02  e@.(f..(........
	0x0310 08 14 80 03 00 00 00 54 06 00 15 00 00 00 00 54  .......T.......T
	0x0320 a0 a4 00 44 04 00 00 1a 84 00 c0 02 05 4c 80 03  ...D.........L..
	0x0330 06 00 15 00 07 00 15 00 08 00 15 00 00 00 00 54  ...............T
	0x0340 80 3c 00 44 00 00 40 03 04 40 80 03 05 00 00 1a  .<.D..@..@......
	0x0350 a5 00 c0 02 06 04 80 03 00 00 00 54 07 4c 80 03  ...........T.L..
	0x0360 87 20 c0 29 07 00 00 1a e7 00 c0 02 87 00 c0 29  . .)...........)
	0x0370 85 00 15 00 04 00 00 1a 84 00 c0 02 60 60 c2 29  ............``.)
	0x0380 66 20 c1 28 66 e0 c0 29 60 d4 00 29 64 00 c2 29  f .(f..)`..)d..)
	0x0390 65 20 c2 29 60 dc 00 29 7d 00 c5 28 a4 03 c0 28  e .)`..)}..(...(
	0x03a0 81 00 00 4c 65 e0 c0 28 67 00 c2 28 66 d4 00 2a  ...Le..(g..(f..*
	0x03b0 64 60 c2 28 68 20 c2 28 61 00 c0 28 63 40 c5 02  d`.(h .(a..(c@..
	0x03c0 20 00 00 4c 88 00 c0 28 85 20 c0 28 04 01 15 00   ..L...(. .(....
	0x03d0 06 00 15 00 07 00 15 00 00 00 00 54 a0 f4 00 44  ...........T...D
	0x03e0 06 00 00 1a c6 00 c0 02 66 a0 c2 29 64 c0 c2 29  ........f..)d..)
	0x03f0 66 a0 c2 02 66 20 c5 29 06 0c 80 03 66 dc 00 29  f...f .)....f..)
	0x0400 85 00 15 00 04 00 00 1a 84 00 c0 02 00 00 00 54  ...............T
	0x0410 c0 60 00 40 60 60 c2 29 64 20 c1 28 64 e0 c0 29  .`.@``.)d .(d..)
	0x0420 60 d4 00 29 66 00 c2 29 67 20 c2 29 04 04 80 03  `..)f..)g .)....
	0x0430 64 dc 00 29 7d 20 c5 28 a4 03 c0 28 81 00 00 4c  d..)} .(...(...L
	0x0440 60 dc 00 29 7d 00 c5 28 a4 03 c0 28 81 00 00 4c  `..)}..(...(...L
	0x0450 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x0460 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x0470 00 00 00 54 64 60 c2 29 66 20 c1 28 66 e0 c0 29  ...Td`.)f .(f..)
	0x0480 60 d4 00 29 60 00 c2 29 60 20 c2 29 06 04 80 03  `..)`..)` .)....
	0x0490 66 dc 00 29 7d 20 c5 28 a6 03 c0 28 c1 00 00 4c  f..)} .(...(...L
	0x04a0 60 dc 00 29 7d 00 c5 28 a6 03 c0 28 c1 00 00 4c  `..)}..(...(...L
	0x04b0 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x04c0 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x04d0 60 60 c2 29 64 20 c1 28 64 e0 c0 29 60 d4 00 29  ``.)d .(d..)`..)
	0x04e0 65 00 c2 29 66 20 c2 29 60 dc 00 29 7d 00 c5 28  e..)f .)`..)}..(
	0x04f0 a4 03 c0 28 81 00 00 4c 65 e0 c0 28 67 00 c2 28  ...(...Le..(g..(
	0x0500 66 d4 00 2a 64 60 c2 28 68 20 c2 28 61 00 c0 28  f..*d`.(h .(a..(
	0x0510 63 40 c5 02 20 00 00 4c 60 60 c2 29 64 e0 c0 29  c@.. ..L``.)d..)
	0x0520 60 d4 00 29 65 00 c2 29 64 80 c2 28 64 20 c2 29  `..)e..)d..(d .)
	0x0530 60 dc 00 29 7d 00 c5 28 a4 03 c0 28 81 00 00 4c  `..)}..(...(...L
	0x0540 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x0550 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x0560 60 60 c2 29 64 e0 c0 29 04 04 80 03 64 d4 00 29  ``.)d..)....d..)
	0x0570 64 40 c1 28 64 00 c2 29 64 80 c2 28 64 20 c2 29  d@.(d..)d..(d .)
	0x0580 60 dc 00 29 7d 00 c5 28 a4 03 c0 28 81 00 00 4c  `..)}..(...(...L
	0x0590 65 e0 c0 28 67 00 c2 28 66 d4 00 2a 64 60 c2 28  e..(g..(f..*d`.(
	0x05a0 68 20 c2 28 61 00 c0 28 63 40 c5 02 20 00 00 4c  h .(a..(c@.. ..L
	0x05b0 60 60 c2 29 60 e0 c0 29 60 d4 00 29 66 00 c2 29  ``.)`..)`..)f..)
	0x05c0 67 20 c2 29 65 e0 c0 28 69 d4 00 2a 64 60 c2 28  g .)e..(i..*d`.(
	0x05d0 e8 00 15 00 c7 00 15 00 26 01 15 00 61 00 c0 28  ........&...a..(
	0x05e0 63 40 c5 02 20 00 00 4c 00 00 00 54 64 60 c2 28  c@.. ..L...Td`.(
	0x05f0 65 e0 c0 28 66 d4 00 2a 67 00 c2 28 68 20 c2 28  e..(f..*g..(h .(
	0x0600 61 00 c0 28 63 40 c5 02 20 00 00 4c 64 20 c0 29  a..(c@.. ..Ld .)
	0x0610 65 40 c0 29 66 60 c0 29 60 80 c0 2b 3f 00 15 00  e@.)f`.)`..+?...
	0x0620 00 00 00 54 64 20 c0 28 65 40 c0 28 66 60 c0 28  ...Td .(e@.(f`.(
	0x0630 60 80 80 2b ff cf f9 53                          `..+...S
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:*errors.errorString+0
	rel 0+0 t=R_USEIFACE type:*os.File+0
	rel 84+4 t=R_LOONG64_ADDR_HI go:string."compare"+0
	rel 88+4 t=R_LOONG64_ADDR_LO go:string."compare"+0
	rel 96+4 t=R_CALLLOONG64 os.MkdirTemp+0
	rel 104+4 t=R_LOONG64_ADDR_HI github.com/go-pdfkit/conformance/compare.draw.deferwrap1+0
	rel 108+4 t=R_LOONG64_ADDR_LO github.com/go-pdfkit/conformance/compare.draw.deferwrap1+0
	rel 156+4 t=R_LOONG64_ADDR_HI go:string."p"+0
	rel 160+4 t=R_LOONG64_ADDR_LO go:string."p"+0
	rel 192+4 t=R_CALLLOONG64 path/filepath.join+0
	rel 204+4 t=R_CALLLOONG64 time.Now+0
	rel 232+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 236+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 240+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 264+4 t=R_CALLLOONG64 fmt.Sprint+0
	rel 288+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 292+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 296+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 320+4 t=R_CALLLOONG64 fmt.Sprint+0
	rel 344+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 348+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 352+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 376+4 t=R_CALLLOONG64 fmt.Sprint+0
	rel 388+4 t=R_LOONG64_ADDR_HI type:[10]string+0
	rel 392+4 t=R_LOONG64_ADDR_LO type:[10]string+0
	rel 396+4 t=R_CALLLOONG64 runtime.newobject+0
	rel 408+4 t=R_LOONG64_ADDR_HI go:string."-cropbox"+0
	rel 412+4 t=R_LOONG64_ADDR_LO go:string."-cropbox"+0
	rel 428+4 t=R_LOONG64_ADDR_HI go:string."-r"+0
	rel 432+4 t=R_LOONG64_ADDR_LO go:string."-r"+0
	rel 448+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 452+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 468+4 t=R_CALLLOONG64 runtime.gcWriteBarrier1+0
	rel 488+4 t=R_LOONG64_ADDR_HI go:string."-f"+0
	rel 492+4 t=R_LOONG64_ADDR_LO go:string."-f"+0
	rel 508+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 512+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 528+4 t=R_CALLLOONG64 runtime.gcWriteBarrier1+0
	rel 548+4 t=R_LOONG64_ADDR_HI go:string."-l"+0
	rel 552+4 t=R_LOONG64_ADDR_LO go:string."-l"+0
	rel 568+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 572+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 588+4 t=R_CALLLOONG64 runtime.gcWriteBarrier1+0
	rel 612+4 t=R_LOONG64_ADDR_HI go:string."-png"+0
	rel 616+4 t=R_LOONG64_ADDR_LO go:string."-png"+0
	rel 632+4 t=R_LOONG64_ADDR_HI runtime.writeBarrier+0
	rel 636+4 t=R_LOONG64_ADDR_LO runtime.writeBarrier+0
	rel 656+4 t=R_CALLLOONG64 runtime.gcWriteBarrier2+0
	rel 692+4 t=R_LOONG64_ADDR_HI github.com/go-pdfkit/conformance/compare.popplerCommand+0
	rel 696+4 t=R_LOONG64_ADDR_LO github.com/go-pdfkit/conformance/compare.popplerCommand+0
	rel 712+0 t=R_CALLIND +0
	rel 740+4 t=R_CALLLOONG64 time.Since+0
	rel 776+4 t=R_LOONG64_ADDR_HI go:string."*.png"+0
	rel 780+4 t=R_LOONG64_ADDR_LO go:string."*.png"+0
	rel 788+4 t=R_CALLLOONG64 runtime.concatstring2+0
	rel 796+4 t=R_CALLLOONG64 path/filepath.globWithLimit+0
	rel 804+4 t=R_LOONG64_ADDR_HI go:string."it wrote no picture"+0
	rel 808+4 t=R_LOONG64_ADDR_LO go:string."it wrote no picture"+0
	rel 828+4 t=R_CALLLOONG64 fmt.errorf+0
	rel 844+4 t=R_LOONG64_ADDR_HI type:errors.errorString+0
	rel 848+4 t=R_LOONG64_ADDR_LO type:errors.errorString+0
	rel 856+4 t=R_CALLLOONG64 runtime.mallocgcSmallScanNoHeaderSC2+0
	rel 868+4 t=R_LOONG64_ADDR_HI go:string."it wrote no picture"+0
	rel 872+4 t=R_LOONG64_ADDR_LO go:string."it wrote no picture"+0
	rel 884+4 t=R_LOONG64_ADDR_HI go:itab.*errors.errorString,error+0
	rel 888+4 t=R_LOONG64_ADDR_LO go:itab.*errors.errorString,error+0
	rel 928+0 t=R_CALLIND +0
	rel 984+4 t=R_CALLLOONG64 os.OpenFile+0
	rel 992+4 t=R_LOONG64_ADDR_HI github.com/go-pdfkit/conformance/compare.draw.deferwrap2+0
	rel 996+4 t=R_LOONG64_ADDR_LO github.com/go-pdfkit/conformance/compare.draw.deferwrap2+0
	rel 1028+4 t=R_LOONG64_ADDR_HI go:itab.*os.File,io.Reader+0
	rel 1032+4 t=R_LOONG64_ADDR_LO go:itab.*os.File,io.Reader+0
	rel 1036+4 t=R_CALLLOONG64 image/png.Decode+0
	rel 1084+0 t=R_CALLIND +0
	rel 1100+0 t=R_CALLIND +0
	rel 1136+4 t=R_CALLLOONG64 github.com/go-gfx/gfx/raster.FromImage+0
	rel 1180+0 t=R_CALLIND +0
	rel 1196+0 t=R_CALLIND +0
	rel 1268+0 t=R_CALLIND +0
	rel 1340+0 t=R_CALLIND +0
	rel 1420+0 t=R_CALLIND +0
	rel 1512+4 t=R_CALLLOONG64 runtime.deferreturn+0
	rel 1568+4 t=R_CALLLOONG64 runtime.morestack_noctxt+0
