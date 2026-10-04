github.com/go-gfx/gfx/color.TestSkimageLabToRGBOutOfGamut STEXT size=1040 align=0x0 args=0x8 locals=0x148 funcid=0x0
	0x0000 00000 (/src/color/skimage_test.go:113)	TEXT	github.com/go-gfx/gfx/color.TestSkimageLabToRGBOutOfGamut(SB), ABIInternal, $328-8
	0x0000 00000 (/src/color/skimage_test.go:113)	MOVV	16(g), R20
	0x0004 00004 (/src/color/skimage_test.go:113)	PCDATA	$0, $-2
	0x0004 00004 (/src/color/skimage_test.go:113)	ADDV	$-208, R3, R24
	0x0008 00008 (/src/color/skimage_test.go:113)	SGTU	R24, R20, R20
	0x000c 00012 (/src/color/skimage_test.go:113)	BEQ	R20, 1020
	0x0010 00016 (/src/color/skimage_test.go:113)	PCDATA	$0, $-1
	0x0010 00016 (/src/color/skimage_test.go:113)	PCDATA	$0, $-2
	0x0010 00016 (/src/color/skimage_test.go:113)	MOVV	R1, -336(R3)
	0x0014 00020 (/src/color/skimage_test.go:113)	ADDV	$-336, R3
	0x0018 00024 (/src/color/skimage_test.go:113)	PCDATA	$0, $-1
	0x0018 00024 (/src/color/skimage_test.go:113)	MOVV	R1, (R3)
	0x001c 00028 (/src/color/skimage_test.go:113)	FUNCDATA	$0, gclocals·N+4Fxc1nG9JyHMKPO1y60g==(SB)
	0x001c 00028 (/src/color/skimage_test.go:113)	FUNCDATA	$1, gclocals·Tn1T0KeKuO63xbeMjIwfTQ==(SB)
	0x001c 00028 (/src/color/skimage_test.go:113)	FUNCDATA	$2, github.com/go-gfx/gfx/color.TestSkimageLabToRGBOutOfGamut.stkobj(SB)
	0x001c 00028 (/src/color/skimage_test.go:113)	FUNCDATA	$5, github.com/go-gfx/gfx/color.TestSkimageLabToRGBOutOfGamut.arginfo1(SB)
	0x001c 00028 (/src/color/skimage_test.go:113)	FUNCDATA	$6, github.com/go-gfx/gfx/color.TestSkimageLabToRGBOutOfGamut.argliveinfo(SB)
	0x001c 00028 (/src/color/skimage_test.go:113)	PCDATA	$3, $1
	0x001c 00028 (/src/color/skimage_test.go:116)	MOVV	R4, github.com/go-gfx/gfx/color.t(FP)
	0x0020 00032 (/src/color/skimage_test.go:116)	PCDATA	$3, $-1
	0x0020 00032 (/src/color/skimage_test.go:116)	PCDATA	$0, $-3
	0x0020 00032 (/src/color/skimage_test.go:114)	MOVD	$f64.4049000000000000(SB), F3
	0x0028 00040 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0028 00040 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-184(SP)
	0x002c 00044 (/src/color/skimage_test.go:114)	PCDATA	$0, $-4
	0x002c 00044 (/src/color/skimage_test.go:114)	MOVD	$f64.405e000000000000(SB), F4
	0x0034 00052 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0034 00052 (/src/color/skimage_test.go:114)	MOVD	F4, github.com/go-gfx/gfx/color..autotmp_25-176(SP)
	0x0038 00056 (/src/color/skimage_test.go:114)	PCDATA	$0, $-3
	0x0038 00056 (/src/color/skimage_test.go:114)	MOVD	$f64.c05e000000000000(SB), F5
	0x0040 00064 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0040 00064 (/src/color/skimage_test.go:114)	MOVD	F5, github.com/go-gfx/gfx/color..autotmp_25-168(SP)
	0x0044 00068 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-160(SP)
	0x0048 00072 (/src/color/skimage_test.go:114)	MOVV	R0, F3
	0x004c 00076 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-152(SP)
	0x0050 00080 (/src/color/skimage_test.go:114)	MOVD	F4, github.com/go-gfx/gfx/color..autotmp_25-144(SP)
	0x0054 00084 (/src/color/skimage_test.go:114)	PCDATA	$0, $-4
	0x0054 00084 (/src/color/skimage_test.go:114)	MOVD	$f64.4059000000000000(SB), F3
	0x005c 00092 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x005c 00092 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-136(SP)
	0x0060 00096 (/src/color/skimage_test.go:114)	PCDATA	$0, $-3
	0x0060 00096 (/src/color/skimage_test.go:114)	MOVD	$f64.4054000000000000(SB), F3
	0x0068 00104 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0068 00104 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-128(SP)
	0x006c 00108 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-120(SP)
	0x0070 00112 (/src/color/skimage_test.go:114)	PCDATA	$0, $-4
	0x0070 00112 (/src/color/skimage_test.go:114)	MOVD	$f64.4034000000000000(SB), F3
	0x0078 00120 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0078 00120 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-112(SP)
	0x007c 00124 (/src/color/skimage_test.go:114)	PCDATA	$0, $-3
	0x007c 00124 (/src/color/skimage_test.go:114)	MOVD	$f64.c059000000000000(SB), F3
	0x0084 00132 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0084 00132 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-104(SP)
	0x0088 00136 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-96(SP)
	0x008c 00140 (/src/color/skimage_test.go:114)	PCDATA	$0, $-4
	0x008c 00140 (/src/color/skimage_test.go:114)	MOVD	$f64.4056800000000000(SB), F3
	0x0094 00148 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x0094 00148 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-88(SP)
	0x0098 00152 (/src/color/skimage_test.go:114)	PCDATA	$0, $-3
	0x0098 00152 (/src/color/skimage_test.go:114)	MOVD	$f64.c054000000000000(SB), F4
	0x00a0 00160 (/src/color/skimage_test.go:114)	PCDATA	$0, $-1
	0x00a0 00160 (/src/color/skimage_test.go:114)	MOVD	F4, github.com/go-gfx/gfx/color..autotmp_25-80(SP)
	0x00a4 00164 (/src/color/skimage_test.go:114)	MOVD	F3, github.com/go-gfx/gfx/color..autotmp_25-72(SP)
	0x00a8 00168 (/src/color/skimage_test.go:115)	MOVV	R0, github.com/go-gfx/gfx/color..autotmp_27-271(SP)
	0x00ac 00172 (/src/color/skimage_test.go:115)	MOVV	R0, github.com/go-gfx/gfx/color..autotmp_27-264(SP)
	0x00b0 00176 (/src/color/skimage_test.go:115)	MOVV	$-72, R5
	0x00b4 00180 (/src/color/skimage_test.go:115)	MOVB	R5, github.com/go-gfx/gfx/color..autotmp_27-271(SP)
	0x00b8 00184 (/src/color/skimage_test.go:115)	MOVV	$-1, R5
	0x00bc 00188 (/src/color/skimage_test.go:115)	MOVB	R5, github.com/go-gfx/gfx/color..autotmp_27-269(SP)
	0x00c0 00192 (/src/color/skimage_test.go:115)	MOVV	$-16747373, R5
	0x00c8 00200 (/src/color/skimage_test.go:115)	MOVW	R5, github.com/go-gfx/gfx/color..autotmp_27-268(SP)
	0x00cc 00204 (/src/color/skimage_test.go:115)	MOVV	$71871554610291890, R5
	0x00dc 00220 (/src/color/skimage_test.go:115)	MOVV	R5, github.com/go-gfx/gfx/color..autotmp_27-264(SP)
	0x00e0 00224 (/src/color/skimage_test.go:116)	MOVV	$github.com/go-gfx/gfx/color..autotmp_25-184(SP), R5
	0x00e4 00228 (/src/color/skimage_test.go:116)	MOVV	R0, R6
	0x00e8 00232 (/src/color/skimage_test.go:116)	JMP	256
	0x00ec 00236 (<unknown line number>)	PCALIGN	$16
	0x00f0 00240 (/src/color/skimage_test.go:116)	MOVV	github.com/go-gfx/gfx/color..autotmp_49-56(SP), R8
	0x00f4 00244 (/src/color/skimage_test.go:116)	ADDVU	$24, R8, R5
	0x00f8 00248 (/src/color/skimage_test.go:116)	ADDVU	$1, R7, R6
	0x00fc 00252 (/src/color/skimage_test.go:120)	MOVV	github.com/go-gfx/gfx/color.t(FP), R4
	0x0100 00256 (/src/color/skimage_test.go:116)	MOVV	$5, R7
	0x0104 00260 (/src/color/skimage_test.go:116)	BGE	R6, R7, 1008
	0x0108 00264 (/src/color/skimage_test.go:116)	MOVV	R6, github.com/go-gfx/gfx/color.i-248(SP)
	0x010c 00268 (/src/color/skimage_test.go:116)	MOVV	R5, github.com/go-gfx/gfx/color..autotmp_49-56(SP)
	0x0110 00272 (/src/color/skimage_test.go:116)	MOVD	(R5), F0
	0x0114 00276 (/src/color/skimage_test.go:116)	MOVD	F0, github.com/go-gfx/gfx/color..autotmp_50-216(SP)
	0x0118 00280 (/src/color/skimage_test.go:116)	MOVD	8(R5), F1
	0x011c 00284 (/src/color/skimage_test.go:116)	MOVD	F1, github.com/go-gfx/gfx/color..autotmp_51-224(SP)
	0x0120 00288 (/src/color/skimage_test.go:116)	MOVD	16(R5), F2
	0x0124 00292 (/src/color/skimage_test.go:116)	MOVD	F2, github.com/go-gfx/gfx/color..autotmp_52-232(SP)
	0x0128 00296 (/src/color/skimage_test.go:117)	PCDATA	$1, $1
	0x0128 00296 (/src/color/skimage_test.go:117)	CALL	github.com/go-gfx/gfx/color.SkimageLabToSRGB(SB)
	0x012c 00300 (/src/color/skimage_test.go:117)	MOVD	F2, github.com/go-gfx/gfx/color.v-256(SP)
	0x0130 00304 (<unknown line number>)	NOP
	0x0130 00304 (/src/color/skimage.go:138)	MOVV	R0, F3
	0x0134 00308 (/src/color/skimage.go:138)	CMPGED	F3, F0, FCC0
	0x0138 00312 (/src/color/skimage.go:138)	MOVV	R0, R4
	0x013c 00316 (/src/color/skimage.go:138)	BFPF	324
	0x0140 00320 (/src/color/skimage.go:138)	MOVV	$1, R4
	0x0144 00324 (/src/color/skimage.go:138)	NOP
	0x0144 00324 (/src/color/skimage.go:138)	MOVBU	R4, R4
	0x0148 00328 (/src/color/skimage.go:138)	BEQ	R4, 356
	0x014c 00332 (/src/color/skimage.go:138)	PCDATA	$0, $-4
	0x014c 00332 (/src/color/skimage.go:138)	MOVD	$f64.3ff0000000000000(SB), F4
	0x0154 00340 (/src/color/skimage.go:138)	PCDATA	$0, $-3
	0x0154 00340 (/src/color/skimage.go:138)	MOVD	$f64.406fe00000000000(SB), F5
	0x015c 00348 (/src/color/skimage.go:138)	PCDATA	$0, $-1
	0x015c 00348 (/src/color/skimage_test.go:118)	MOVV	R0, F6
	0x0160 00352 (/src/color/skimage_test.go:118)	JMP	484
	0x0164 00356 (/src/color/skimage_test.go:118)	PCDATA	$0, $-4
	0x0164 00356 (/src/color/skimage.go:141)	MOVD	$f64.3ff0000000000000(SB), F4
	0x016c 00364 (/src/color/skimage.go:141)	PCDATA	$0, $-1
	0x016c 00364 (/src/color/skimage.go:141)	CMPGED	F0, F4, FCC0
	0x0170 00368 (/src/color/skimage.go:141)	MOVV	R0, R6
	0x0174 00372 (/src/color/skimage.go:141)	BFPF	380
	0x0178 00376 (/src/color/skimage.go:141)	MOVV	$1, R6
	0x017c 00380 (/src/color/skimage.go:141)	NOP
	0x017c 00380 (/src/color/skimage.go:141)	MOVBU	R6, R6
	0x0180 00384 (/src/color/skimage.go:141)	BEQ	R6, 408
	0x0184 00388 (/src/color/skimage.go:141)	PCDATA	$0, $-3
	0x0184 00388 (/src/color/skimage.go:141)	MOVD	$f64.406fe00000000000(SB), F5
	0x018c 00396 (/src/color/skimage.go:141)	PCDATA	$0, $-1
	0x018c 00396 (/src/color/skimage.go:141)	MOVV	$-1, R4
	0x0190 00400 (/src/color/skimage_test.go:118)	MOVV	R4, F6
	0x0194 00404 (/src/color/skimage_test.go:118)	JMP	484
	0x0198 00408 (/src/color/skimage_test.go:118)	PCDATA	$0, $-4
	0x0198 00408 (/src/color/skimage.go:144)	MOVBU	runtime.loong64HasLSX(SB), R6
	0x01a0 00416 (/src/color/skimage.go:144)	PCDATA	$0, $-3
	0x01a0 00416 (/src/color/skimage.go:144)	MOVD	$f64.406fe00000000000(SB), F5
	0x01a8 00424 (/src/color/skimage.go:144)	PCDATA	$0, $-1
	0x01a8 00424 (/src/color/skimage.go:144)	MULD	F0, F5, F0
	0x01ac 00428 (/src/color/skimage.go:144)	BEQ	R6, 440
	0x01b0 00432 (/src/color/skimage.go:144)	VFRINTRNED	V0, V6
	0x01b4 00436 (/src/color/skimage.go:144)	JMP	480
	0x01b8 00440 (/src/color/skimage_test.go:117)	MOVD	F1, github.com/go-gfx/gfx/color.g-240(SP)
	0x01bc 00444 (/src/color/skimage.go:144)	CALL	math.RoundToEven(SB)
	0x01c0 00448 (/src/color/skimage.go:138)	MOVD	github.com/go-gfx/gfx/color.g-240(SP), F1
	0x01c4 00452 (/src/color/skimage.go:138)	MOVD	github.com/go-gfx/gfx/color.v-256(SP), F2
	0x01c8 00456 (/src/color/skimage.go:138)	MOVV	R0, F3
	0x01cc 00460 (/src/color/skimage.go:138)	PCDATA	$0, $-4
	0x01cc 00460 (/src/color/skimage.go:138)	MOVD	$f64.3ff0000000000000(SB), F4
	0x01d4 00468 (/src/color/skimage.go:138)	PCDATA	$0, $-3
	0x01d4 00468 (/src/color/skimage.go:138)	MOVD	$f64.406fe00000000000(SB), F5
	0x01dc 00476 (/src/color/skimage.go:138)	PCDATA	$0, $-1
	0x01dc 00476 (/src/color/skimage_test.go:118)	MOVD	F0, F6
	0x01e0 00480 (/src/color/skimage.go:144)	TRUNCDW	F6, F6
	0x01e4 00484 (/src/color/skimage_test.go:118)	MOVD	F6, github.com/go-gfx/gfx/color.~r0-275(SP)
	0x01e8 00488 (<unknown line number>)	NOP
	0x01e8 00488 (/src/color/skimage.go:138)	CMPGED	F3, F1, FCC0
	0x01ec 00492 (/src/color/skimage.go:138)	MOVV	R0, R6
	0x01f0 00496 (/src/color/skimage.go:138)	BFPF	504
	0x01f4 00500 (/src/color/skimage.go:138)	MOVV	$1, R6
	0x01f8 00504 (/src/color/skimage.go:138)	NOP
	0x01f8 00504 (/src/color/skimage.go:138)	MOVBU	R6, R6
	0x01fc 00508 (/src/color/skimage.go:138)	BEQ	R6, 520
	0x0200 00512 (/src/color/skimage_test.go:118)	MOVV	R0, F7
	0x0204 00516 (/src/color/skimage_test.go:118)	JMP	620
	0x0208 00520 (/src/color/skimage.go:141)	CMPGED	F1, F4, FCC0
	0x020c 00524 (/src/color/skimage.go:141)	MOVV	R0, R6
	0x0210 00528 (/src/color/skimage.go:141)	BFPF	536
	0x0214 00532 (/src/color/skimage.go:141)	MOVV	$1, R6
	0x0218 00536 (/src/color/skimage.go:141)	NOP
	0x0218 00536 (/src/color/skimage.go:141)	MOVBU	R6, R6
	0x021c 00540 (/src/color/skimage.go:141)	BEQ	R6, 556
	0x0220 00544 (/src/color/skimage.go:141)	MOVV	$-1, R4
	0x0224 00548 (/src/color/skimage_test.go:118)	MOVV	R4, F7
	0x0228 00552 (/src/color/skimage_test.go:118)	JMP	620
	0x022c 00556 (/src/color/skimage_test.go:118)	PCDATA	$0, $-4
	0x022c 00556 (/src/color/skimage.go:144)	MOVBU	runtime.loong64HasLSX(SB), R6
	0x0234 00564 (/src/color/skimage.go:144)	PCDATA	$0, $-1
	0x0234 00564 (/src/color/skimage.go:144)	MULD	F1, F5, F0
	0x0238 00568 (/src/color/skimage.go:144)	BEQ	R6, 580
	0x023c 00572 (/src/color/skimage.go:144)	VFRINTRNED	V0, V7
	0x0240 00576 (/src/color/skimage.go:144)	JMP	616
	0x0244 00580 (/src/color/skimage.go:144)	CALL	math.RoundToEven(SB)
	0x0248 00584 (/src/color/skimage.go:138)	MOVD	github.com/go-gfx/gfx/color.v-256(SP), F2
	0x024c 00588 (/src/color/skimage.go:138)	MOVV	R0, F3
	0x0250 00592 (/src/color/skimage.go:138)	PCDATA	$0, $-3
	0x0250 00592 (/src/color/skimage.go:138)	MOVD	$f64.3ff0000000000000(SB), F4
	0x0258 00600 (/src/color/skimage.go:138)	PCDATA	$0, $-4
	0x0258 00600 (/src/color/skimage.go:138)	MOVD	$f64.406fe00000000000(SB), F5
	0x0260 00608 (/src/color/skimage.go:138)	PCDATA	$0, $-1
	0x0260 00608 (/src/color/skimage_test.go:118)	MOVD	github.com/go-gfx/gfx/color.~r0-275(SP), F6
	0x0264 00612 (/src/color/skimage_test.go:118)	MOVD	F0, F7
	0x0268 00616 (/src/color/skimage.go:144)	TRUNCDW	F7, F7
	0x026c 00620 (<unknown line number>)	NOP
	0x026c 00620 (/src/color/skimage.go:138)	CMPGED	F3, F2, FCC0
	0x0270 00624 (/src/color/skimage.go:138)	MOVV	R0, R6
	0x0274 00628 (/src/color/skimage.go:138)	BFPF	636
	0x0278 00632 (/src/color/skimage.go:138)	MOVV	$1, R6
	0x027c 00636 (/src/color/skimage.go:138)	NOP
	0x027c 00636 (/src/color/skimage.go:138)	MOVBU	R6, R6
	0x0280 00640 (/src/color/skimage.go:138)	BEQ	R6, 652
	0x0284 00644 (/src/color/skimage_test.go:118)	MOVV	R0, F3
	0x0288 00648 (/src/color/skimage_test.go:118)	JMP	736
	0x028c 00652 (/src/color/skimage.go:141)	CMPGED	F2, F4, FCC0
	0x0290 00656 (/src/color/skimage.go:141)	MOVV	R0, R6
	0x0294 00660 (/src/color/skimage.go:141)	BFPF	668
	0x0298 00664 (/src/color/skimage.go:141)	MOVV	$1, R6
	0x029c 00668 (/src/color/skimage.go:141)	NOP
	0x029c 00668 (/src/color/skimage.go:141)	MOVBU	R6, R6
	0x02a0 00672 (/src/color/skimage.go:141)	BEQ	R6, 688
	0x02a4 00676 (/src/color/skimage.go:141)	MOVV	$-1, R4
	0x02a8 00680 (/src/color/skimage_test.go:118)	MOVV	R4, F3
	0x02ac 00684 (/src/color/skimage_test.go:118)	JMP	736
	0x02b0 00688 (/src/color/skimage_test.go:118)	PCDATA	$0, $-3
	0x02b0 00688 (/src/color/skimage.go:144)	MOVBU	runtime.loong64HasLSX(SB), R6
	0x02b8 00696 (/src/color/skimage.go:144)	PCDATA	$0, $-1
	0x02b8 00696 (/src/color/skimage.go:144)	MULD	F2, F5, F0
	0x02bc 00700 (/src/color/skimage.go:144)	BEQ	R6, 712
	0x02c0 00704 (/src/color/skimage.go:144)	VFRINTRNED	V0, V3
	0x02c4 00708 (/src/color/skimage.go:144)	JMP	732
	0x02c8 00712 (/src/color/skimage_test.go:118)	MOVD	F7, github.com/go-gfx/gfx/color.~r0-276(SP)
	0x02cc 00716 (/src/color/skimage.go:144)	CALL	math.RoundToEven(SB)
	0x02d0 00720 (/src/color/skimage_test.go:118)	MOVD	github.com/go-gfx/gfx/color.~r0-275(SP), F6
	0x02d4 00724 (/src/color/skimage_test.go:118)	MOVD	github.com/go-gfx/gfx/color.~r0-276(SP), F7
	0x02d8 00728 (/src/color/skimage_test.go:118)	MOVD	F0, F3
	0x02dc 00732 (/src/color/skimage.go:144)	TRUNCDW	F3, F3
	0x02e0 00736 (/src/color/skimage_test.go:118)	MOVV	F6, R6
	0x02e4 00740 (/src/color/skimage_test.go:118)	MOVB	R6, github.com/go-gfx/gfx/color.got-274(SP)
	0x02e8 00744 (/src/color/skimage_test.go:118)	MOVV	F7, R7
	0x02ec 00748 (/src/color/skimage_test.go:118)	MOVB	R7, github.com/go-gfx/gfx/color.got-273(SP)
	0x02f0 00752 (/src/color/skimage_test.go:118)	MOVV	F3, R8
	0x02f4 00756 (/src/color/skimage_test.go:118)	MOVB	R8, github.com/go-gfx/gfx/color.got-272(SP)
	0x02f8 00760 (/src/color/skimage_test.go:119)	MOVBU	R7, R7
	0x02fc 00764 (/src/color/skimage_test.go:119)	MOVBU	R6, R6
	0x0300 00768 (/src/color/skimage_test.go:119)	SLLV	$8, R7, R7
	0x0304 00772 (/src/color/skimage_test.go:119)	OR	R7, R6, R6
	0x0308 00776 (/src/color/skimage_test.go:119)	MOVV	github.com/go-gfx/gfx/color.i-248(SP), R7
	0x030c 00780 (/src/color/skimage_test.go:119)	ALSLV	$1, R7, R7, R9
	0x0310 00784 (/src/color/skimage_test.go:119)	MOVV	$github.com/go-gfx/gfx/color..autotmp_27-271(SP), R10
	0x0314 00788 (/src/color/skimage_test.go:119)	MOVHU	(R10)(R9), R11
	0x0318 00792 (/src/color/skimage_test.go:119)	ADDVU	R9, R10, R9
	0x031c 00796 (/src/color/skimage_test.go:119)	MOVV	R9, github.com/go-gfx/gfx/color..autotmp_53-64(SP)
	0x0320 00800 (/src/color/skimage_test.go:119)	MOVHU	R6, R6
	0x0324 00804 (/src/color/skimage_test.go:119)	BNE	R6, R11, 820
	0x0328 00808 (/src/color/skimage_test.go:119)	MOVBU	2(R9), R6
	0x032c 00812 (/src/color/skimage_test.go:119)	MOVBU	R8, R8
	0x0330 00816 (/src/color/skimage_test.go:119)	BEQ	R8, R6, 240
	0x0334 00820 (/src/color/skimage_test.go:120)	MOVV	$github.com/go-gfx/gfx/color..autotmp_23-48(SP), R6
	0x0338 00824 (/src/color/skimage_test.go:120)	MOVV	R0, (R6)
	0x033c 00828 (/src/color/skimage_test.go:120)	MOVV	R0, 8(R6)
	0x0340 00832 (/src/color/skimage_test.go:120)	MOVV	R0, 16(R6)
	0x0344 00836 (/src/color/skimage_test.go:120)	MOVV	R0, 24(R6)
	0x0348 00840 (/src/color/skimage_test.go:120)	MOVV	R0, 32(R6)
	0x034c 00844 (/src/color/skimage_test.go:120)	MOVV	R0, 40(R6)
	0x0350 00848 (/src/color/skimage_test.go:120)	MOVD	github.com/go-gfx/gfx/color..autotmp_50-216(SP), F0
	0x0354 00852 (/src/color/skimage_test.go:120)	MOVD	F0, github.com/go-gfx/gfx/color..autotmp_34-208(SP)
	0x0358 00856 (/src/color/skimage_test.go:120)	MOVD	github.com/go-gfx/gfx/color..autotmp_51-224(SP), F0
	0x035c 00860 (/src/color/skimage_test.go:120)	MOVD	F0, github.com/go-gfx/gfx/color..autotmp_34-200(SP)
	0x0360 00864 (/src/color/skimage_test.go:120)	MOVD	github.com/go-gfx/gfx/color..autotmp_52-232(SP), F0
	0x0364 00868 (/src/color/skimage_test.go:120)	MOVD	F0, github.com/go-gfx/gfx/color..autotmp_34-192(SP)
	0x0368 00872 (/src/color/skimage_test.go:120)	MOVV	$type:github.com/go-gfx/gfx/color.Lab(SB), R4
	0x0370 00880 (/src/color/skimage_test.go:120)	MOVV	$github.com/go-gfx/gfx/color..autotmp_34-208(SP), R5
	0x0374 00884 (/src/color/skimage_test.go:120)	PCDATA	$1, $2
	0x0374 00884 (/src/color/skimage_test.go:120)	CALL	runtime.convTnoptr(SB)
	0x0378 00888 (/src/color/skimage_test.go:120)	MOVV	$type:github.com/go-gfx/gfx/color.Lab(SB), R6
	0x0380 00896 (/src/color/skimage_test.go:120)	MOVV	R6, github.com/go-gfx/gfx/color..autotmp_23-48(SP)
	0x0384 00900 (/src/color/skimage_test.go:120)	MOVV	R4, github.com/go-gfx/gfx/color..autotmp_23-40(SP)
	0x0388 00904 (/src/color/skimage_test.go:120)	MOVV	$type:[3]uint8(SB), R4
	0x0390 00912 (/src/color/skimage_test.go:120)	MOVV	$github.com/go-gfx/gfx/color.got-274(SP), R5
	0x0394 00916 (/src/color/skimage_test.go:120)	CALL	runtime.convTnoptr(SB)
	0x0398 00920 (/src/color/skimage_test.go:120)	MOVV	$type:[3]uint8(SB), R6
	0x03a0 00928 (/src/color/skimage_test.go:120)	MOVV	R6, github.com/go-gfx/gfx/color..autotmp_23-32(SP)
	0x03a4 00932 (/src/color/skimage_test.go:120)	MOVV	R4, github.com/go-gfx/gfx/color..autotmp_23-24(SP)
	0x03a8 00936 (/src/color/skimage_test.go:120)	MOVV	R6, R4
	0x03ac 00940 (/src/color/skimage_test.go:120)	MOVV	github.com/go-gfx/gfx/color..autotmp_53-64(SP), R5
	0x03b0 00944 (/src/color/skimage_test.go:120)	PCDATA	$1, $3
	0x03b0 00944 (/src/color/skimage_test.go:120)	CALL	runtime.convTnoptr(SB)
	0x03b4 00948 (/src/color/skimage_test.go:120)	MOVV	$type:[3]uint8(SB), R6
	0x03bc 00956 (/src/color/skimage_test.go:120)	MOVV	R6, github.com/go-gfx/gfx/color..autotmp_23-16(SP)
	0x03c0 00960 (/src/color/skimage_test.go:120)	MOVV	R4, github.com/go-gfx/gfx/color..autotmp_23-8(SP)
	0x03c4 00964 (/src/color/skimage_test.go:120)	MOVV	github.com/go-gfx/gfx/color.t(FP), R4
	0x03c8 00968 (/src/color/skimage_test.go:120)	PCDATA	$0, $-2
	0x03c8 00968 (/src/color/skimage_test.go:120)	MOVB	(R4), R30
	0x03cc 00972 (/src/color/skimage_test.go:120)	PCDATA	$0, $-1
	0x03cc 00972 (/src/color/skimage_test.go:120)	MOVV	$go:string."lab2rgb %+v = %v, want %v"(SB), R5
	0x03d4 00980 (/src/color/skimage_test.go:120)	MOVV	$25, R6
	0x03d8 00984 (/src/color/skimage_test.go:120)	MOVV	$github.com/go-gfx/gfx/color..autotmp_23-48(SP), R7
	0x03dc 00988 (/src/color/skimage_test.go:120)	MOVV	$3, R8
	0x03e0 00992 (/src/color/skimage_test.go:120)	MOVV	R8, R9
	0x03e4 00996 (/src/color/skimage_test.go:120)	PCDATA	$1, $1
	0x03e4 00996 (/src/color/skimage_test.go:120)	CALL	testing.(*common).Errorf(SB)
	0x03e8 01000 (/src/color/skimage_test.go:116)	MOVV	github.com/go-gfx/gfx/color.i-248(SP), R7
	0x03ec 01004 (/src/color/skimage_test.go:120)	JMP	240
	0x03f0 01008 (/src/color/skimage_test.go:123)	MOVV	(R3), R1
	0x03f4 01012 (/src/color/skimage_test.go:123)	ADDV	$336, R3
	0x03f8 01016 (/src/color/skimage_test.go:123)	JMP	(R1)
	0x03fc 01020 (/src/color/skimage_test.go:123)	NOP
	0x03fc 01020 (/src/color/skimage_test.go:113)	PCDATA	$1, $-1
	0x03fc 01020 (/src/color/skimage_test.go:113)	PCDATA	$0, $-2
	0x03fc 01020 (/src/color/skimage_test.go:113)	MOVV	R4, 8(R3)
	0x0400 01024 (/src/color/skimage_test.go:113)	MOVV	R1, R31
	0x0404 01028 (/src/color/skimage_test.go:113)	CALL	runtime.morestack_noctxt(SB)
	0x0408 01032 (/src/color/skimage_test.go:113)	PCDATA	$0, $-1
	0x0408 01032 (/src/color/skimage_test.go:113)	MOVV	8(R3), R4
	0x040c 01036 (/src/color/skimage_test.go:113)	JMP	0
	0x0000 d4 42 c0 28 78 c0 fc 02 94 e2 12 00 80 f2 03 40  .B.(x..........@
	0x0010 61 c0 fa 29 63 c0 fa 02 61 00 c0 29 64 60 c5 29  a..)c...a..)d`.)
	0x0020 1e 00 00 1a c3 03 80 2b 63 60 c2 2b 1e 00 00 1a  .......+c`.+....
	0x0030 c4 03 80 2b 64 80 c2 2b 1e 00 00 1a c5 03 80 2b  ...+d..+.......+
	0x0040 65 a0 c2 2b 63 c0 c2 2b 03 a8 14 01 63 e0 c2 2b  e..+c..+....c..+
	0x0050 64 00 c3 2b 1e 00 00 1a c3 03 80 2b 63 20 c3 2b  d..+.......+c .+
	0x0060 1e 00 00 1a c3 03 80 2b 63 40 c3 2b 63 60 c3 2b  .......+c@.+c`.+
	0x0070 1e 00 00 1a c3 03 80 2b 63 80 c3 2b 1e 00 00 1a  .......+c..+....
	0x0080 c3 03 80 2b 63 a0 c3 2b 63 c0 c3 2b 1e 00 00 1a  ...+c..+c..+....
	0x0090 c3 03 80 2b 63 e0 c3 2b 1e 00 00 1a c4 03 80 2b  ...+c..+.......+
	0x00a0 64 00 c4 2b 63 20 c4 2b 60 04 c1 29 60 20 c1 29  d..+c .+`..)` .)
	0x00b0 05 e0 fe 02 65 04 01 29 05 fc ff 02 65 0c 01 29  ....e..)....e..)
	0x00c0 e5 00 fe 15 a5 4c 92 03 65 10 81 29 c5 00 a4 14  .....L..e..)....
	0x00d0 a5 c8 92 03 85 d9 ea 17 a5 3c 00 03 65 20 c1 29  .........<..e .)
	0x00e0 65 60 c2 02 06 00 15 00 00 18 00 50 00 00 40 03  e`.........P..@.
	0x00f0 68 60 c4 28 05 61 c0 02 e6 04 c0 02 64 60 c5 28  h`.(.a......d`.(
	0x0100 07 14 80 03 c7 ec 02 64 66 60 c1 29 65 60 c4 29  .......df`.)e`.)
	0x0110 a0 00 80 2b 60 e0 c1 2b a1 20 80 2b 61 c0 c1 2b  ...+`..+. .+a..+
	0x0120 a2 40 80 2b 62 a0 c1 2b 00 00 00 54 62 40 c1 2b  .@.+b..+...Tb@.+
	0x0130 03 a8 14 01 00 8c 23 0c 04 00 15 00 00 08 00 48  ......#........H
	0x0140 04 04 80 03 84 fc 43 03 80 1c 00 40 1e 00 00 1a  ......C....@....
	0x0150 c4 03 80 2b 1e 00 00 1a c5 03 80 2b 06 a8 14 01  ...+.......+....
	0x0160 00 84 00 50 1e 00 00 1a c4 03 80 2b 80 80 23 0c  ...P.......+..#.
	0x0170 06 00 15 00 00 08 00 48 06 04 80 03 c6 fc 43 03  .......H......C.
	0x0180 c0 18 00 40 1e 00 00 1a c5 03 80 2b 04 fc ff 02  ...@.......+....
	0x0190 86 a8 14 01 00 50 00 50 1e 00 00 1a c6 03 00 2a  .....P.P.......*
	0x01a0 1e 00 00 1a c5 03 80 2b a0 00 05 01 c0 0c 00 40  .......+.......@
	0x01b0 06 78 9d 72 00 2c 00 50 61 80 c1 2b 00 00 00 54  .x.r.,.Pa..+...T
	0x01c0 61 80 81 2b 62 40 81 2b 03 a8 14 01 1e 00 00 1a  a..+b@.+........
	0x01d0 c4 03 80 2b 1e 00 00 1a c5 03 80 2b 06 98 14 01  ...+.......+....
	0x01e0 c6 88 1a 01 66 f4 c0 2b 20 8c 23 0c 06 00 15 00  ....f..+ .#.....
	0x01f0 00 08 00 48 06 04 80 03 c6 fc 43 03 c0 0c 00 40  ...H......C....@
	0x0200 07 a8 14 01 00 68 00 50 80 84 23 0c 06 00 15 00  .....h.P..#.....
	0x0210 00 08 00 48 06 04 80 03 c6 fc 43 03 c0 10 00 40  ...H......C....@
	0x0220 04 fc ff 02 87 a8 14 01 00 44 00 50 1e 00 00 1a  .........D.P....
	0x0230 c6 03 00 2a a0 04 05 01 c0 0c 00 40 07 78 9d 72  ...*.......@.x.r
	0x0240 00 28 00 50 00 00 00 54 62 40 81 2b 03 a8 14 01  .(.P...Tb@.+....
	0x0250 1e 00 00 1a c4 03 80 2b 1e 00 00 1a c5 03 80 2b  .......+.......+
	0x0260 66 f4 80 2b 07 98 14 01 e7 88 1a 01 40 8c 23 0c  f..+........@.#.
	0x0270 06 00 15 00 00 08 00 48 06 04 80 03 c6 fc 43 03  .......H......C.
	0x0280 c0 0c 00 40 03 a8 14 01 00 58 00 50 80 88 23 0c  ...@.....X.P..#.
	0x0290 06 00 15 00 00 08 00 48 06 04 80 03 c6 fc 43 03  .......H......C.
	0x02a0 c0 10 00 40 04 fc ff 02 83 a8 14 01 00 34 00 50  ...@.........4.P
	0x02b0 1e 00 00 1a c6 03 00 2a a0 08 05 01 c0 0c 00 40  .......*.......@
	0x02c0 03 78 9d 72 00 18 00 50 67 f0 c0 2b 00 00 00 54  .x.r...Pg..+...T
	0x02d0 66 f4 80 2b 67 f0 80 2b 03 98 14 01 63 88 1a 01  f..+g..+....c...
	0x02e0 c6 b8 14 01 66 f8 00 29 e7 b8 14 01 67 fc 00 29  ....f..)....g..)
	0x02f0 68 b8 14 01 68 00 01 29 e7 fc 43 03 c6 fc 43 03  h...h..)..C...C.
	0x0300 e7 20 41 00 c6 1c 15 00 67 60 c1 28 e9 1c 2c 00  . A.....g`.(..,.
	0x0310 6a 04 c1 02 4b 25 24 38 49 a5 10 00 69 40 c4 29  j...K%$8I...i@.)
	0x0320 c6 00 cf 00 cb 10 00 5c 26 09 00 2a 08 fd 43 03  .......\&..*..C.
	0x0330 06 c1 fd 5b 66 80 c4 02 c0 00 c0 29 c0 20 c0 29  ...[f......). .)
	0x0340 c0 40 c0 29 c0 60 c0 29 c0 80 c0 29 c0 a0 c0 29  .@.).`.)...)...)
	0x0350 60 e0 81 2b 60 00 c2 2b 60 c0 81 2b 60 20 c2 2b  `..+`..+`..+` .+
	0x0360 60 a0 81 2b 60 40 c2 2b 04 00 00 1a 84 00 c0 02  `..+`@.+........
	0x0370 65 00 c2 02 00 00 00 54 06 00 00 1a c6 00 c0 02  e......T........
	0x0380 66 80 c4 29 64 a0 c4 29 04 00 00 1a 84 00 c0 02  f..)d..)........
	0x0390 65 f8 c0 02 00 00 00 54 06 00 00 1a c6 00 c0 02  e......T........
	0x03a0 66 c0 c4 29 64 e0 c4 29 c4 00 15 00 65 40 c4 28  f..)d..)....e@.(
	0x03b0 00 00 00 54 06 00 00 1a c6 00 c0 02 66 00 c5 29  ...T........f..)
	0x03c0 64 20 c5 29 64 60 c5 28 9e 00 00 28 05 00 00 1a  d .)d`.(...(....
	0x03d0 a5 00 c0 02 06 64 80 03 67 80 c4 02 08 0c 80 03  .....d..g.......
	0x03e0 09 01 15 00 00 00 00 54 67 60 c1 28 ff 07 fd 53  .......Tg`.(...S
	0x03f0 61 00 c0 28 63 40 c5 02 20 00 00 4c 64 20 c0 29  a..(c@.. ..Ld .)
	0x0400 3f 00 15 00 00 00 00 54 64 20 c0 28 ff f7 fb 53  ?......Td .(...S
	rel 0+0 t=R_USEIFACE type:github.com/go-gfx/gfx/color.Lab+0
	rel 0+0 t=R_USEIFACE type:[3]uint8+0
	rel 0+0 t=R_USEIFACE type:[3]uint8+0
	rel 32+4 t=R_LOONG64_ADDR_HI $f64.4049000000000000+0
	rel 36+4 t=R_LOONG64_ADDR_LO $f64.4049000000000000+0
	rel 44+4 t=R_LOONG64_ADDR_HI $f64.405e000000000000+0
	rel 48+4 t=R_LOONG64_ADDR_LO $f64.405e000000000000+0
	rel 56+4 t=R_LOONG64_ADDR_HI $f64.c05e000000000000+0
	rel 60+4 t=R_LOONG64_ADDR_LO $f64.c05e000000000000+0
	rel 84+4 t=R_LOONG64_ADDR_HI $f64.4059000000000000+0
	rel 88+4 t=R_LOONG64_ADDR_LO $f64.4059000000000000+0
	rel 96+4 t=R_LOONG64_ADDR_HI $f64.4054000000000000+0
	rel 100+4 t=R_LOONG64_ADDR_LO $f64.4054000000000000+0
	rel 112+4 t=R_LOONG64_ADDR_HI $f64.4034000000000000+0
	rel 116+4 t=R_LOONG64_ADDR_LO $f64.4034000000000000+0
	rel 124+4 t=R_LOONG64_ADDR_HI $f64.c059000000000000+0
	rel 128+4 t=R_LOONG64_ADDR_LO $f64.c059000000000000+0
	rel 140+4 t=R_LOONG64_ADDR_HI $f64.4056800000000000+0
	rel 144+4 t=R_LOONG64_ADDR_LO $f64.4056800000000000+0
	rel 152+4 t=R_LOONG64_ADDR_HI $f64.c054000000000000+0
	rel 156+4 t=R_LOONG64_ADDR_LO $f64.c054000000000000+0
	rel 296+4 t=R_CALLLOONG64 github.com/go-gfx/gfx/color.SkimageLabToSRGB+0
	rel 332+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 336+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 340+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 344+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 356+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 360+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 388+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 392+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 408+4 t=R_LOONG64_ADDR_HI runtime.loong64HasLSX+0
	rel 412+4 t=R_LOONG64_ADDR_LO runtime.loong64HasLSX+0
	rel 416+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 420+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 444+4 t=R_CALLLOONG64 math.RoundToEven+0
	rel 460+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 464+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 468+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 472+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 556+4 t=R_LOONG64_ADDR_HI runtime.loong64HasLSX+0
	rel 560+4 t=R_LOONG64_ADDR_LO runtime.loong64HasLSX+0
	rel 580+4 t=R_CALLLOONG64 math.RoundToEven+0
	rel 592+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 596+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 600+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 604+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 688+4 t=R_LOONG64_ADDR_HI runtime.loong64HasLSX+0
	rel 692+4 t=R_LOONG64_ADDR_LO runtime.loong64HasLSX+0
	rel 716+4 t=R_CALLLOONG64 math.RoundToEven+0
	rel 872+4 t=R_LOONG64_ADDR_HI type:github.com/go-gfx/gfx/color.Lab+0
	rel 876+4 t=R_LOONG64_ADDR_LO type:github.com/go-gfx/gfx/color.Lab+0
	rel 884+4 t=R_CALLLOONG64 runtime.convTnoptr+0
	rel 888+4 t=R_LOONG64_ADDR_HI type:github.com/go-gfx/gfx/color.Lab+0
	rel 892+4 t=R_LOONG64_ADDR_LO type:github.com/go-gfx/gfx/color.Lab+0
	rel 904+4 t=R_LOONG64_ADDR_HI type:[3]uint8+0
	rel 908+4 t=R_LOONG64_ADDR_LO type:[3]uint8+0
	rel 916+4 t=R_CALLLOONG64 runtime.convTnoptr+0
	rel 920+4 t=R_LOONG64_ADDR_HI type:[3]uint8+0
	rel 924+4 t=R_LOONG64_ADDR_LO type:[3]uint8+0
	rel 944+4 t=R_CALLLOONG64 runtime.convTnoptr+0
	rel 948+4 t=R_LOONG64_ADDR_HI type:[3]uint8+0
	rel 952+4 t=R_LOONG64_ADDR_LO type:[3]uint8+0
	rel 972+4 t=R_LOONG64_ADDR_HI go:string."lab2rgb %+v = %v, want %v"+0
	rel 976+4 t=R_LOONG64_ADDR_LO go:string."lab2rgb %+v = %v, want %v"+0
	rel 996+4 t=R_CALLLOONG64 testing.(*common).Errorf+0
	rel 1028+4 t=R_CALLLOONG64 runtime.morestack_noctxt+0
