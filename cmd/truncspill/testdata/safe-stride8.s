github.com/go-gfx/gfx/color.TestTheWhitePointIsWhatKeepsANeutralNeutral STEXT size=2072 align=0x0 args=0x8 locals=0x90 funcid=0x0
	0x0000 00000 (/src/color/calibrated_test.go:343)	TEXT	github.com/go-gfx/gfx/color.TestTheWhitePointIsWhatKeepsANeutralNeutral(SB), ABIInternal, $144-8
	0x0000 00000 (/src/color/calibrated_test.go:343)	MOVV	16(g), R20
	0x0004 00004 (/src/color/calibrated_test.go:343)	PCDATA	$0, $-2
	0x0004 00004 (/src/color/calibrated_test.go:343)	ADDV	$-24, R3, R24
	0x0008 00008 (/src/color/calibrated_test.go:343)	SGTU	R24, R20, R20
	0x000c 00012 (/src/color/calibrated_test.go:343)	BEQ	R20, 2052
	0x0010 00016 (/src/color/calibrated_test.go:343)	PCDATA	$0, $-1
	0x0010 00016 (/src/color/calibrated_test.go:343)	PCDATA	$0, $-2
	0x0010 00016 (/src/color/calibrated_test.go:343)	MOVV	R1, -152(R3)
	0x0014 00020 (/src/color/calibrated_test.go:343)	ADDV	$-152, R3
	0x0018 00024 (/src/color/calibrated_test.go:343)	PCDATA	$0, $-1
	0x0018 00024 (/src/color/calibrated_test.go:343)	MOVV	R1, (R3)
	0x001c 00028 (/src/color/calibrated_test.go:343)	FUNCDATA	$0, gclocals·mwFurZyQFy+ezr0Dhah66w==(SB)
	0x001c 00028 (/src/color/calibrated_test.go:343)	FUNCDATA	$1, gclocals·8IN/omyuME9BBSsEpGN3TA==(SB)
	0x001c 00028 (/src/color/calibrated_test.go:343)	FUNCDATA	$2, github.com/go-gfx/gfx/color.TestTheWhitePointIsWhatKeepsANeutralNeutral.stkobj(SB)
	0x001c 00028 (/src/color/calibrated_test.go:343)	FUNCDATA	$5, github.com/go-gfx/gfx/color.TestTheWhitePointIsWhatKeepsANeutralNeutral.arginfo1(SB)
	0x001c 00028 (/src/color/calibrated_test.go:343)	FUNCDATA	$6, github.com/go-gfx/gfx/color.TestTheWhitePointIsWhatKeepsANeutralNeutral.argliveinfo(SB)
	0x001c 00028 (/src/color/calibrated_test.go:343)	PCDATA	$3, $1
	0x001c 00028 (/src/color/cmyk.go:111)	MOVV	R4, github.com/go-gfx/gfx/color.t(FP)
	0x0020 00032 (/src/color/cmyk.go:111)	PCDATA	$3, $-1
	0x0020 00032 (/src/color/cmyk.go:111)	PCDATA	$0, $-3
	0x0020 00032 (/src/color/calibrated_test.go:347)	MOVD	$f64.4049000000000000(SB), F0
	0x0028 00040 (/src/color/calibrated_test.go:347)	PCDATA	$0, $-1
	0x0028 00040 (/src/color/calibrated_test.go:347)	MOVV	R0, F1
	0x002c 00044 (/src/color/calibrated_test.go:347)	MOVD	F1, F2
	0x0030 00048 (/src/color/calibrated_test.go:347)	PCDATA	$0, $-4
	0x0030 00048 (/src/color/calibrated_test.go:347)	MOVD	$f64.3feedb8bac710cb3(SB), F3
	0x0038 00056 (/src/color/calibrated_test.go:347)	PCDATA	$0, $-3
	0x0038 00056 (/src/color/calibrated_test.go:347)	MOVD	$f64.3ff0000000000000(SB), F4
	0x0040 00064 (/src/color/calibrated_test.go:347)	PCDATA	$0, $-4
	0x0040 00064 (/src/color/calibrated_test.go:347)	MOVD	$f64.3fea67381d7dbf48(SB), F5
	0x0048 00072 (/src/color/calibrated_test.go:347)	PCDATA	$0, $-1
	0x0048 00072 (/src/color/calibrated_test.go:347)	PCDATA	$1, $0
	0x0048 00072 (/src/color/calibrated_test.go:347)	CALL	github.com/go-gfx/gfx/color.LabToSRGBWP(SB)
	0x004c 00076 (<unknown line number>)	NOP
	0x004c 00076 (<unknown line number>)	NOP
	0x004c 00076 (/src/color/cmyk.go:111)	MOVV	R0, F6
	0x0050 00080 (/src/color/cmyk.go:111)	CMPGTD	F6, F0, FCC0
	0x0054 00084 (/src/color/cmyk.go:111)	MOVV	R0, R4
	0x0058 00088 (/src/color/cmyk.go:111)	BFPF	96
	0x005c 00092 (/src/color/cmyk.go:111)	MOVV	$1, R4
	0x0060 00096 (/src/color/cmyk.go:111)	NOP
	0x0060 00096 (/src/color/cmyk.go:111)	MOVBU	R4, R4
	0x0064 00100 (/src/color/cmyk.go:111)	BEQ	R4, 112
	0x0068 00104 (/src/color/cmyk.go:111)	MOVV	R0, F3
	0x006c 00108 (/src/color/calibrated_test.go:116)	JMP	160
	0x0070 00112 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-3
	0x0070 00112 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F4
	0x0078 00120 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0078 00120 (/src/color/cmyk.go:114)	CMPGTD	F0, F4, FCC0
	0x007c 00124 (/src/color/cmyk.go:114)	MOVV	R0, R5
	0x0080 00128 (/src/color/cmyk.go:114)	BFPF	136
	0x0084 00132 (/src/color/cmyk.go:114)	MOVV	$1, R5
	0x0088 00136 (/src/color/cmyk.go:114)	NOP
	0x0088 00136 (/src/color/cmyk.go:114)	MOVBU	R5, R5
	0x008c 00140 (/src/color/cmyk.go:114)	BEQ	R5, 156
	0x0090 00144 (/src/color/cmyk.go:114)	PCDATA	$0, $-4
	0x0090 00144 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F3
	0x0098 00152 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0098 00152 (/src/color/calibrated_test.go:116)	JMP	160
	0x009c 00156 (/src/color/calibrated_test.go:116)	MOVD	F0, F3
	0x00a0 00160 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-3
	0x00a0 00160 (/src/color/calibrated_test.go:116)	MOVD	$f64.406fe00000000000(SB), F7
	0x00a8 00168 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-1
	0x00a8 00168 (/src/color/calibrated_test.go:116)	MULD	F3, F7, F8
	0x00ac 00172 (/src/math/unsafe.go:35)	MOVV	F8, R5
	0x00b0 00176 (/src/math/floor.go:101)	SRLV	$52, R5, R6
	0x00b4 00180 (<unknown line number>)	NOP
	0x00b4 00180 (/src/math/floor.go:100)	NOOP
	0x00b8 00184 (/src/math/floor.go:101)	AND	$2047, R6, R6
	0x00bc 00188 (/src/math/floor.go:102)	MOVV	$1023, R7
	0x00c0 00192 (/src/math/floor.go:102)	BGEU	R6, R7, 232
	0x00c4 00196 (/src/math/floor.go:104)	MOVV	$-9223372036854775808, R8
	0x00c8 00200 (/src/math/floor.go:104)	AND	R5, R8, R5
	0x00cc 00204 (/src/math/floor.go:105)	XOR	$1022, R6, R6
	0x00d0 00208 (/src/math/floor.go:105)	BNE	R6, 224
	0x00d4 00212 (/src/math/floor.go:106)	MOVV	$4607182418800017408, R6
	0x00d8 00216 (/src/math/floor.go:106)	OR	R5, R6, R5
	0x00dc 00220 (/src/math/floor.go:106)	JMP	312
	0x00e0 00224 (/src/math/floor.go:106)	MOVV	$4607182418800017408, R6
	0x00e4 00228 (/src/math/floor.go:105)	JMP	312
	0x00e8 00232 (/src/math/floor.go:108)	MOVV	$1075, R8
	0x00ec 00236 (/src/math/floor.go:108)	BGEU	R6, R8, 284
	0x00f0 00240 (/src/math/floor.go:114)	ADDVU	$-1023, R6, R6
	0x00f4 00244 (/src/math/floor.go:115)	MOVV	$2251799813685248, R8
	0x0100 00256 (/src/math/floor.go:115)	SRLV	R6, R8, R9
	0x0104 00260 (/src/math/floor.go:115)	ADDVU	R9, R5, R9
	0x0108 00264 (/src/math/floor.go:116)	MOVV	$4503599627370495, R10
	0x0110 00272 (/src/math/floor.go:116)	SRLV	R6, R10, R6
	0x0114 00276 (/src/math/floor.go:116)	ANDN	R6, R9, R5
	0x0118 00280 (/src/math/floor.go:116)	JMP	304
	0x011c 00284 (/src/math/floor.go:116)	MOVV	$2251799813685248, R8
	0x0128 00296 (/src/math/floor.go:116)	MOVV	$4503599627370495, R10
	0x0130 00304 (<unknown line number>)	MOVV	$4607182418800017408, R6
	0x0134 00308 (<unknown line number>)	MOVV	$-9223372036854775808, R8
	0x0138 00312 (<unknown line number>)	NOP
	0x0138 00312 (<unknown line number>)	NOP
	0x0138 00312 (/src/math/floor.go:118)	NOOP
	0x013c 00316 (/src/color/cmyk.go:111)	CMPGTD	F6, F2, FCC0
	0x0140 00320 (/src/color/cmyk.go:111)	MOVV	R0, R9
	0x0144 00324 (/src/color/cmyk.go:111)	BFPF	332
	0x0148 00328 (/src/color/cmyk.go:111)	MOVV	$1, R9
	0x014c 00332 (/src/color/cmyk.go:111)	NOP
	0x014c 00332 (/src/color/cmyk.go:111)	MOVBU	R9, R9
	0x0150 00336 (/src/color/cmyk.go:111)	BEQ	R9, 348
	0x0154 00340 (/src/color/cmyk.go:111)	MOVV	R0, F3
	0x0158 00344 (/src/color/calibrated_test.go:116)	JMP	396
	0x015c 00348 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-4
	0x015c 00348 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F4
	0x0164 00356 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0164 00356 (/src/color/cmyk.go:114)	CMPGTD	F2, F4, FCC0
	0x0168 00360 (/src/color/cmyk.go:114)	MOVV	R0, R10
	0x016c 00364 (/src/color/cmyk.go:114)	BFPF	372
	0x0170 00368 (/src/color/cmyk.go:114)	MOVV	$1, R10
	0x0174 00372 (/src/color/cmyk.go:114)	NOP
	0x0174 00372 (/src/color/cmyk.go:114)	MOVBU	R10, R10
	0x0178 00376 (/src/color/cmyk.go:114)	BEQ	R10, 392
	0x017c 00380 (/src/color/cmyk.go:114)	PCDATA	$0, $-3
	0x017c 00380 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F3
	0x0184 00388 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0184 00388 (/src/color/calibrated_test.go:116)	JMP	396
	0x0188 00392 (/src/color/calibrated_test.go:116)	MOVD	F2, F3
	0x018c 00396 (/src/color/calibrated_test.go:116)	MULD	F7, F3, F8
	0x0190 00400 (/src/math/unsafe.go:35)	MOVV	F8, R10
	0x0194 00404 (/src/math/floor.go:101)	SRLV	$52, R10, R11
	0x0198 00408 (<unknown line number>)	NOP
	0x0198 00408 (/src/math/floor.go:100)	NOOP
	0x019c 00412 (/src/math/floor.go:101)	AND	$2047, R11, R11
	0x01a0 00416 (/src/math/floor.go:102)	BGEU	R11, R7, 440
	0x01a4 00420 (/src/math/floor.go:104)	AND	R8, R10, R10
	0x01a8 00424 (/src/math/floor.go:105)	XOR	$1022, R11, R11
	0x01ac 00428 (/src/math/floor.go:105)	BNE	R11, 512
	0x01b0 00432 (/src/math/floor.go:106)	OR	R6, R10, R10
	0x01b4 00436 (/src/math/floor.go:106)	JMP	512
	0x01b8 00440 (/src/math/floor.go:108)	MOVV	$1075, R12
	0x01bc 00444 (/src/math/floor.go:108)	BGEU	R11, R12, 492
	0x01c0 00448 (/src/math/floor.go:114)	ADDVU	$-1023, R11, R11
	0x01c4 00452 (/src/math/floor.go:115)	MOVV	$2251799813685248, R12
	0x01d0 00464 (/src/math/floor.go:115)	SRLV	R11, R12, R13
	0x01d4 00468 (/src/math/floor.go:115)	ADDVU	R13, R10, R13
	0x01d8 00472 (/src/math/floor.go:116)	MOVV	$4503599627370495, R14
	0x01e0 00480 (/src/math/floor.go:116)	SRLV	R11, R14, R11
	0x01e4 00484 (/src/math/floor.go:116)	ANDN	R11, R13, R10
	0x01e8 00488 (/src/math/floor.go:116)	JMP	512
	0x01ec 00492 (/src/math/floor.go:116)	MOVV	$2251799813685248, R12
	0x01f8 00504 (/src/math/floor.go:116)	MOVV	$4503599627370495, R14
	0x0200 00512 (/src/math/unsafe.go:41)	MOVV	R5, F8
	0x0204 00516 (/src/color/calibrated_test.go:116)	TRUNCDV	F8, F8
	0x0208 00520 (/src/math/unsafe.go:41)	MOVV	R10, F9
	0x020c 00524 (/src/color/calibrated_test.go:116)	TRUNCDV	F9, F9
	0x0210 00528 (/src/color/calibrated_test.go:348)	MOVV	F8, R5
	0x0214 00532 (/src/color/calibrated_test.go:348)	MOVV	F9, R10
	0x0218 00536 (/src/color/calibrated_test.go:348)	SUBVU	R10, R5, R5
	0x021c 00540 (/src/math/floor.go:118)	NOOP
	0x0220 00544 (/src/color/calibrated_test.go:348)	ADDVU	$1, R5, R5
	0x0224 00548 (/src/color/calibrated_test.go:348)	MOVV	$2, R10
	0x0228 00552 (/src/color/calibrated_test.go:348)	BGEU	R10, R5, 1240
	0x022c 00556 (<unknown line number>)	NOP
	0x022c 00556 (<unknown line number>)	NOP
	0x022c 00556 (/src/color/cmyk.go:111)	BEQ	R4, 568
	0x0230 00560 (/src/color/cmyk.go:111)	MOVV	R0, F0
	0x0234 00564 (/src/color/calibrated_test.go:116)	JMP	608
	0x0238 00568 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-4
	0x0238 00568 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F3
	0x0240 00576 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0240 00576 (/src/color/cmyk.go:114)	CMPGTD	F0, F3, FCC0
	0x0244 00580 (/src/color/cmyk.go:114)	MOVV	R0, R5
	0x0248 00584 (/src/color/cmyk.go:114)	BFPF	592
	0x024c 00588 (/src/color/cmyk.go:114)	MOVV	$1, R5
	0x0250 00592 (/src/color/cmyk.go:114)	NOP
	0x0250 00592 (/src/color/cmyk.go:114)	MOVBU	R5, R5
	0x0254 00596 (/src/color/cmyk.go:114)	BEQ	R5, 608
	0x0258 00600 (/src/color/cmyk.go:114)	PCDATA	$0, $-3
	0x0258 00600 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F0
	0x0260 00608 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0260 00608 (/src/color/calibrated_test.go:116)	MULD	F7, F0, F0
	0x0264 00612 (/src/math/unsafe.go:35)	MOVV	F0, R5
	0x0268 00616 (/src/math/floor.go:101)	SRLV	$52, R5, R10
	0x026c 00620 (<unknown line number>)	NOP
	0x026c 00620 (/src/math/floor.go:100)	NOOP
	0x0270 00624 (/src/math/floor.go:101)	AND	$2047, R10, R10
	0x0274 00628 (/src/math/floor.go:102)	BGEU	R10, R7, 652
	0x0278 00632 (/src/math/floor.go:104)	AND	R8, R5, R5
	0x027c 00636 (/src/math/floor.go:105)	XOR	$1022, R10, R10
	0x0280 00640 (/src/math/floor.go:105)	BNE	R10, 724
	0x0284 00644 (/src/math/floor.go:106)	OR	R6, R5, R5
	0x0288 00648 (/src/math/floor.go:106)	JMP	724
	0x028c 00652 (/src/math/floor.go:108)	MOVV	$1075, R4
	0x0290 00656 (/src/math/floor.go:108)	BGEU	R10, R4, 704
	0x0294 00660 (/src/math/floor.go:114)	ADDVU	$-1023, R10, R10
	0x0298 00664 (/src/math/floor.go:115)	MOVV	$2251799813685248, R11
	0x02a4 00676 (/src/math/floor.go:115)	SRLV	R10, R11, R12
	0x02a8 00680 (/src/math/floor.go:115)	ADDVU	R12, R5, R12
	0x02ac 00684 (/src/math/floor.go:116)	MOVV	$4503599627370495, R13
	0x02b4 00692 (/src/math/floor.go:116)	SRLV	R10, R13, R10
	0x02b8 00696 (/src/math/floor.go:116)	ANDN	R10, R12, R5
	0x02bc 00700 (/src/math/floor.go:116)	JMP	724
	0x02c0 00704 (/src/math/floor.go:116)	MOVV	$2251799813685248, R11
	0x02cc 00716 (/src/math/floor.go:116)	MOVV	$4503599627370495, R13
	0x02d4 00724 (<unknown line number>)	NOP
	0x02d4 00724 (<unknown line number>)	NOP
	0x02d4 00724 (/src/math/floor.go:118)	NOOP
	0x02d8 00728 (/src/color/cmyk.go:111)	CMPGTD	F6, F1, FCC0
	0x02dc 00732 (/src/color/cmyk.go:111)	MOVV	R0, R10
	0x02e0 00736 (/src/color/cmyk.go:111)	BFPF	744
	0x02e4 00740 (/src/color/cmyk.go:111)	MOVV	$1, R10
	0x02e8 00744 (/src/color/cmyk.go:111)	NOP
	0x02e8 00744 (/src/color/cmyk.go:111)	MOVBU	R10, R10
	0x02ec 00748 (/src/color/cmyk.go:111)	BEQ	R10, 760
	0x02f0 00752 (/src/color/cmyk.go:111)	MOVV	R0, F1
	0x02f4 00756 (/src/color/calibrated_test.go:116)	JMP	800
	0x02f8 00760 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-4
	0x02f8 00760 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F0
	0x0300 00768 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0300 00768 (/src/color/cmyk.go:114)	CMPGTD	F1, F0, FCC0
	0x0304 00772 (/src/color/cmyk.go:114)	MOVV	R0, R10
	0x0308 00776 (/src/color/cmyk.go:114)	BFPF	784
	0x030c 00780 (/src/color/cmyk.go:114)	MOVV	$1, R10
	0x0310 00784 (/src/color/cmyk.go:114)	NOP
	0x0310 00784 (/src/color/cmyk.go:114)	MOVBU	R10, R10
	0x0314 00788 (/src/color/cmyk.go:114)	BEQ	R10, 800
	0x0318 00792 (/src/color/cmyk.go:114)	PCDATA	$0, $-3
	0x0318 00792 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F1
	0x0320 00800 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0320 00800 (/src/color/calibrated_test.go:116)	MULD	F7, F1, F0
	0x0324 00804 (/src/math/unsafe.go:35)	MOVV	F0, R10
	0x0328 00808 (/src/math/floor.go:101)	SRLV	$52, R10, R11
	0x032c 00812 (<unknown line number>)	NOP
	0x032c 00812 (/src/math/floor.go:100)	NOOP
	0x0330 00816 (/src/math/floor.go:101)	AND	$2047, R11, R11
	0x0334 00820 (/src/math/floor.go:102)	BGEU	R11, R7, 844
	0x0338 00824 (/src/math/floor.go:104)	AND	R8, R10, R10
	0x033c 00828 (/src/math/floor.go:105)	XOR	$1022, R11, R11
	0x0340 00832 (/src/math/floor.go:105)	BNE	R11, 916
	0x0344 00836 (/src/math/floor.go:106)	OR	R6, R10, R10
	0x0348 00840 (/src/math/floor.go:106)	JMP	916
	0x034c 00844 (/src/math/floor.go:108)	MOVV	$1075, R4
	0x0350 00848 (/src/math/floor.go:108)	BGEU	R11, R4, 896
	0x0354 00852 (/src/math/floor.go:114)	ADDVU	$-1023, R11, R11
	0x0358 00856 (/src/math/floor.go:115)	MOVV	$2251799813685248, R12
	0x0364 00868 (/src/math/floor.go:115)	SRLV	R11, R12, R13
	0x0368 00872 (/src/math/floor.go:115)	ADDVU	R13, R10, R13
	0x036c 00876 (/src/math/floor.go:116)	MOVV	$4503599627370495, R14
	0x0374 00884 (/src/math/floor.go:116)	SRLV	R11, R14, R11
	0x0378 00888 (/src/math/floor.go:116)	ANDN	R11, R13, R10
	0x037c 00892 (/src/math/floor.go:116)	JMP	916
	0x0380 00896 (/src/math/floor.go:116)	MOVV	$2251799813685248, R12
	0x038c 00908 (/src/math/floor.go:116)	MOVV	$4503599627370495, R14
	0x0394 00916 (<unknown line number>)	NOP
	0x0394 00916 (<unknown line number>)	NOP
	0x0394 00916 (/src/math/floor.go:118)	NOOP
	0x0398 00920 (/src/color/cmyk.go:111)	BEQ	R9, 932
	0x039c 00924 (/src/color/cmyk.go:111)	MOVV	R0, F2
	0x03a0 00928 (/src/color/calibrated_test.go:116)	JMP	972
	0x03a4 00932 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-4
	0x03a4 00932 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F0
	0x03ac 00940 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x03ac 00940 (/src/color/cmyk.go:114)	CMPGTD	F2, F0, FCC0
	0x03b0 00944 (/src/color/cmyk.go:114)	MOVV	R0, R9
	0x03b4 00948 (/src/color/cmyk.go:114)	BFPF	956
	0x03b8 00952 (/src/color/cmyk.go:114)	MOVV	$1, R9
	0x03bc 00956 (/src/color/cmyk.go:114)	NOP
	0x03bc 00956 (/src/color/cmyk.go:114)	MOVBU	R9, R9
	0x03c0 00960 (/src/color/cmyk.go:114)	BEQ	R9, 972
	0x03c4 00964 (/src/color/cmyk.go:114)	PCDATA	$0, $-3
	0x03c4 00964 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F2
	0x03cc 00972 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x03cc 00972 (/src/color/calibrated_test.go:116)	MULD	F7, F2, F0
	0x03d0 00976 (/src/math/unsafe.go:35)	MOVV	F0, R9
	0x03d4 00980 (/src/math/floor.go:101)	SRLV	$52, R9, R11
	0x03d8 00984 (<unknown line number>)	NOP
	0x03d8 00984 (/src/math/floor.go:100)	NOOP
	0x03dc 00988 (/src/math/floor.go:101)	AND	$2047, R11, R11
	0x03e0 00992 (/src/math/floor.go:102)	BGEU	R11, R7, 1016
	0x03e4 00996 (/src/math/floor.go:104)	AND	R8, R9, R7
	0x03e8 01000 (/src/math/floor.go:105)	XOR	$1022, R11, R8
	0x03ec 01004 (/src/math/floor.go:105)	BNE	R8, 1068
	0x03f0 01008 (/src/math/floor.go:106)	OR	R6, R7, R7
	0x03f4 01012 (/src/math/floor.go:106)	JMP	1068
	0x03f8 01016 (/src/math/floor.go:108)	MOVV	$1075, R4
	0x03fc 01020 (/src/math/floor.go:108)	BGEU	R11, R4, 1064
	0x0400 01024 (/src/math/floor.go:114)	ADDVU	$-1023, R11, R6
	0x0404 01028 (/src/math/floor.go:115)	MOVV	$2251799813685248, R7
	0x0410 01040 (/src/math/floor.go:115)	SRLV	R6, R7, R7
	0x0414 01044 (/src/math/floor.go:115)	ADDVU	R7, R9, R7
	0x0418 01048 (/src/math/floor.go:116)	MOVV	$4503599627370495, R8
	0x0420 01056 (/src/math/floor.go:116)	SRLV	R6, R8, R6
	0x0424 01060 (/src/math/floor.go:116)	ANDN	R6, R7, R9
	0x0428 01064 (/src/color/calibrated_test.go:116)	MOVV	R9, R7
	0x042c 01068 (/src/math/unsafe.go:41)	MOVV	R5, F0
	0x0430 01072 (/src/color/calibrated_test.go:116)	TRUNCDV	F0, F0
	0x0434 01076 (/src/math/unsafe.go:41)	MOVV	R10, F1
	0x0438 01080 (/src/color/calibrated_test.go:116)	TRUNCDV	F1, F1
	0x043c 01084 (/src/color/calibrated_test.go:116)	MOVD	F1, github.com/go-gfx/gfx/color.~r0-88(SP)
	0x0440 01088 (/src/math/unsafe.go:41)	MOVV	R7, F1
	0x0444 01092 (/src/color/calibrated_test.go:116)	TRUNCDV	F1, F1
	0x0448 01096 (/src/color/calibrated_test.go:116)	MOVD	F1, github.com/go-gfx/gfx/color.~r0-96(SP)
	0x044c 01100 (/src/math/floor.go:118)	NOOP
	0x0450 01104 (/src/color/calibrated_test.go:349)	MOVV	$github.com/go-gfx/gfx/color..autotmp_119-48(SP), R5
	0x0454 01108 (/src/color/calibrated_test.go:349)	MOVV	R0, (R5)
	0x0458 01112 (/src/color/calibrated_test.go:349)	MOVV	R0, 8(R5)
	0x045c 01116 (/src/color/calibrated_test.go:349)	MOVV	R0, 16(R5)
	0x0460 01120 (/src/color/calibrated_test.go:349)	MOVV	R0, 24(R5)
	0x0464 01124 (/src/color/calibrated_test.go:349)	MOVV	R0, 32(R5)
	0x0468 01128 (/src/color/calibrated_test.go:349)	MOVV	R0, 40(R5)
	0x046c 01132 (/src/color/calibrated_test.go:349)	MOVV	F0, R4
	0x0470 01136 (/src/color/calibrated_test.go:349)	PCDATA	$1, $1
	0x0470 01136 (/src/color/calibrated_test.go:349)	CALL	runtime.convT64(SB)
	0x0474 01140 (/src/color/calibrated_test.go:349)	MOVV	$type:int(SB), R5
	0x047c 01148 (/src/color/calibrated_test.go:349)	MOVV	R5, github.com/go-gfx/gfx/color..autotmp_119-48(SP)
	0x0480 01152 (/src/color/calibrated_test.go:349)	MOVV	R4, github.com/go-gfx/gfx/color..autotmp_119-40(SP)
	0x0484 01156 (/src/color/calibrated_test.go:349)	MOVV	github.com/go-gfx/gfx/color.~r0-88(SP), R4
	0x0488 01160 (/src/color/calibrated_test.go:349)	CALL	runtime.convT64(SB)
	0x048c 01164 (/src/color/calibrated_test.go:349)	MOVV	$type:int(SB), R5
	0x0494 01172 (/src/color/calibrated_test.go:349)	MOVV	R5, github.com/go-gfx/gfx/color..autotmp_119-32(SP)
	0x0498 01176 (/src/color/calibrated_test.go:349)	MOVV	R4, github.com/go-gfx/gfx/color..autotmp_119-24(SP)
	0x049c 01180 (/src/color/calibrated_test.go:349)	MOVV	github.com/go-gfx/gfx/color.~r0-96(SP), R4
	0x04a0 01184 (/src/color/calibrated_test.go:349)	CALL	runtime.convT64(SB)
	0x04a4 01188 (/src/color/calibrated_test.go:349)	MOVV	$type:int(SB), R5
	0x04ac 01196 (/src/color/calibrated_test.go:349)	MOVV	R5, github.com/go-gfx/gfx/color..autotmp_119-16(SP)
	0x04b0 01200 (/src/color/calibrated_test.go:349)	MOVV	R4, github.com/go-gfx/gfx/color..autotmp_119-8(SP)
	0x04b4 01204 (/src/color/calibrated_test.go:349)	MOVV	github.com/go-gfx/gfx/color.t(FP), R4
	0x04b8 01208 (/src/color/calibrated_test.go:349)	PCDATA	$0, $-2
	0x04b8 01208 (/src/color/calibrated_test.go:349)	MOVB	(R4), R30
	0x04bc 01212 (/src/color/calibrated_test.go:349)	PCDATA	$0, $-1
	0x04bc 01212 (/src/color/calibrated_test.go:349)	MOVV	$go:string."a neutral Lab under D50 came back with a cast: %d %d %d"(SB), R5
	0x04c4 01220 (/src/color/calibrated_test.go:349)	MOVV	$55, R6
	0x04c8 01224 (/src/color/calibrated_test.go:349)	MOVV	$github.com/go-gfx/gfx/color..autotmp_119-48(SP), R7
	0x04cc 01228 (/src/color/calibrated_test.go:349)	MOVV	$3, R8
	0x04d0 01232 (/src/color/calibrated_test.go:349)	MOVV	R8, R9
	0x04d4 01236 (/src/color/calibrated_test.go:349)	PCDATA	$1, $0
	0x04d4 01236 (/src/color/calibrated_test.go:349)	CALL	testing.(*common).Errorf(SB)
	0x04d8 01240 (/src/color/calibrated_test.go:349)	PCDATA	$0, $-4
	0x04d8 01240 (/src/color/calibrated_test.go:353)	MOVD	$f64.4049000000000000(SB), F0
	0x04e0 01248 (/src/color/calibrated_test.go:353)	PCDATA	$0, $-1
	0x04e0 01248 (/src/color/calibrated_test.go:353)	MOVV	R0, F1
	0x04e4 01252 (/src/color/calibrated_test.go:353)	MOVD	F1, F2
	0x04e8 01256 (/src/color/calibrated_test.go:353)	PCDATA	$0, $-3
	0x04e8 01256 (/src/color/calibrated_test.go:353)	MOVD	$f64.3feedb8bac710cb3(SB), F3
	0x04f0 01264 (/src/color/calibrated_test.go:353)	PCDATA	$0, $-4
	0x04f0 01264 (/src/color/calibrated_test.go:353)	MOVD	$f64.3ff0000000000000(SB), F4
	0x04f8 01272 (/src/color/calibrated_test.go:353)	PCDATA	$0, $-3
	0x04f8 01272 (/src/color/calibrated_test.go:353)	MOVD	$f64.3fea67381d7dbf48(SB), F5
	0x0500 01280 (/src/color/calibrated_test.go:353)	PCDATA	$0, $-1
	0x0500 01280 (/src/color/calibrated_test.go:353)	CALL	github.com/go-gfx/gfx/color.LabToXYZWP(SB)
	0x0504 01284 (/src/color/calibrated_test.go:353)	PCDATA	$0, $-4
	0x0504 01284 (/src/color/space.go:63)	MOVD	$f64.3ff8981e8a2ec28b(SB), F6
	0x050c 01292 (/src/color/space.go:63)	PCDATA	$0, $-1
	0x050c 01292 (/src/color/space.go:63)	MULD	F1, F6, F6
	0x0510 01296 (/src/color/space.go:63)	PCDATA	$0, $-3
	0x0510 01296 (/src/color/space.go:63)	MOVD	$f64.4009ec7340697c9b(SB), F7
	0x0518 01304 (/src/color/space.go:63)	PCDATA	$0, $-1
	0x0518 01304 (/src/color/space.go:63)	FMSUBD	F6, F7, F0, F6
	0x051c 01308 (/src/color/space.go:63)	PCDATA	$0, $-4
	0x051c 01308 (/src/color/space.go:63)	MOVD	$f64.3fdfe7f03ec1dcaf(SB), F7
	0x0524 01316 (/src/color/space.go:63)	PCDATA	$0, $-1
	0x0524 01316 (/src/color/space.go:63)	FNMSUBD	F6, F2, F7, F6
	0x0528 01320 (/src/color/space.go:63)	PCDATA	$0, $-3
	0x0528 01320 (/src/color/space.go:65)	MOVD	$f64.3fca1d854c04bb51(SB), F7
	0x0530 01328 (/src/color/space.go:65)	PCDATA	$0, $-1
	0x0530 01328 (/src/color/space.go:65)	MULD	F1, F7, F7
	0x0534 01332 (/src/color/space.go:65)	PCDATA	$0, $-4
	0x0534 01332 (/src/color/space.go:65)	MOVD	$f64.3fac7d4aae79fb6f(SB), F8
	0x053c 01340 (/src/color/space.go:65)	PCDATA	$0, $-1
	0x053c 01340 (/src/color/space.go:65)	FMSUBD	F7, F8, F0, F7
	0x0540 01344 (<unknown line number>)	NOP
	0x0540 01344 (<unknown line number>)	NOP
	0x0540 01344 (<unknown line number>)	PCDATA	$0, $-3
	0x0540 01344 (/src/color/space.go:65)	MOVD	$f64.3ff0ea64f8a81cea(SB), F8
	0x0548 01352 (/src/color/space.go:65)	PCDATA	$0, $-1
	0x0548 01352 (/src/color/space.go:65)	FMADDD	F7, F2, F8, F0
	0x054c 01356 (/src/color/space.go:65)	PCDATA	$0, $-4
	0x054c 01356 (/src/color/space.go:31)	MOVD	$f64.3f69a5c37387b719(SB), F7
	0x0554 01364 (/src/color/space.go:31)	PCDATA	$0, $-1
	0x0554 01364 (/src/color/space.go:31)	CMPGED	F7, F6, FCC0
	0x0558 01368 (/src/color/space.go:31)	MOVV	R0, R4
	0x055c 01372 (/src/color/space.go:31)	BFPF	1380
	0x0560 01376 (/src/color/space.go:31)	MOVV	$1, R4
	0x0564 01380 (/src/color/space.go:31)	NOP
	0x0564 01380 (/src/color/space.go:31)	MOVBU	R4, R4
	0x0568 01384 (/src/color/space.go:31)	BEQ	R4, 1404
	0x056c 01388 (/src/color/space.go:31)	PCDATA	$0, $-3
	0x056c 01388 (/src/color/space.go:32)	MOVD	$f64.4029d70a3d70a3d7(SB), F2
	0x0574 01396 (/src/color/space.go:32)	PCDATA	$0, $-1
	0x0574 01396 (/src/color/space.go:32)	MULD	F6, F2, F3
	0x0578 01400 (/src/color/calibrated_test.go:354)	JMP	1464
	0x057c 01404 (/src/color/space.go:65)	MOVD	F0, github.com/go-gfx/gfx/color.b-72(SP)
	0x0580 01408 (<unknown line number>)	NOP
	0x0580 01408 (/src/math/pow.go:52)	MOVD	F6, F0
	0x0584 01412 (/src/math/pow.go:52)	PCDATA	$0, $-4
	0x0584 01412 (/src/math/pow.go:52)	MOVD	$f64.3fdaaaaaaaaaaaab(SB), F1
	0x058c 01420 (/src/math/pow.go:52)	PCDATA	$0, $-1
	0x058c 01420 (/src/math/pow.go:52)	CALL	math.pow(SB)
	0x0590 01424 (/src/math/pow.go:52)	PCDATA	$0, $-3
	0x0590 01424 (/src/color/space.go:34)	MOVD	$f64.3ff0e147ae147ae1(SB), F2
	0x0598 01432 (/src/color/space.go:34)	PCDATA	$0, $-4
	0x0598 01432 (/src/color/space.go:34)	MOVD	$f64.3fac28f5c28f5c29(SB), F3
	0x05a0 01440 (/src/color/space.go:34)	PCDATA	$0, $-1
	0x05a0 01440 (/src/color/space.go:34)	FMSUBD	F3, F0, F2, F3
	0x05a4 01444 (/src/color/space.go:31)	MOVD	github.com/go-gfx/gfx/color.b-72(SP), F0
	0x05a8 01448 (/src/color/space.go:31)	PCDATA	$0, $-3
	0x05a8 01448 (/src/color/space.go:31)	MOVD	$f64.4029d70a3d70a3d7(SB), F2
	0x05b0 01456 (/src/color/space.go:31)	PCDATA	$0, $-4
	0x05b0 01456 (/src/color/space.go:31)	MOVD	$f64.3f69a5c37387b719(SB), F7
	0x05b8 01464 (/src/color/space.go:31)	PCDATA	$0, $-1
	0x05b8 01464 (<unknown line number>)	NOP
	0x05b8 01464 (<unknown line number>)	NOP
	0x05b8 01464 (/src/color/cmyk.go:111)	MOVV	R0, F4
	0x05bc 01468 (/src/color/cmyk.go:111)	CMPGTD	F4, F3, FCC0
	0x05c0 01472 (/src/color/cmyk.go:111)	MOVV	R0, R8
	0x05c4 01476 (/src/color/cmyk.go:111)	BFPF	1484
	0x05c8 01480 (/src/color/cmyk.go:111)	MOVV	$1, R8
	0x05cc 01484 (/src/color/cmyk.go:111)	NOP
	0x05cc 01484 (/src/color/cmyk.go:111)	MOVBU	R8, R8
	0x05d0 01488 (/src/color/cmyk.go:111)	BEQ	R8, 1500
	0x05d4 01492 (/src/color/cmyk.go:111)	MOVV	R0, F3
	0x05d8 01496 (/src/color/calibrated_test.go:116)	JMP	1540
	0x05dc 01500 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-3
	0x05dc 01500 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F5
	0x05e4 01508 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x05e4 01508 (/src/color/cmyk.go:114)	CMPGTD	F3, F5, FCC0
	0x05e8 01512 (/src/color/cmyk.go:114)	MOVV	R0, R8
	0x05ec 01516 (/src/color/cmyk.go:114)	BFPF	1524
	0x05f0 01520 (/src/color/cmyk.go:114)	MOVV	$1, R8
	0x05f4 01524 (/src/color/cmyk.go:114)	NOP
	0x05f4 01524 (/src/color/cmyk.go:114)	MOVBU	R8, R8
	0x05f8 01528 (/src/color/cmyk.go:114)	BEQ	R8, 1540
	0x05fc 01532 (/src/color/cmyk.go:114)	PCDATA	$0, $-4
	0x05fc 01532 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F3
	0x0604 01540 (/src/color/cmyk.go:114)	PCDATA	$0, $-3
	0x0604 01540 (/src/color/calibrated_test.go:116)	MOVD	$f64.406fe00000000000(SB), F5
	0x060c 01548 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-1
	0x060c 01548 (/src/color/calibrated_test.go:116)	MULD	F5, F3, F3
	0x0610 01552 (/src/math/unsafe.go:35)	MOVV	F3, R8
	0x0614 01556 (/src/math/floor.go:101)	SRLV	$52, R8, R9
	0x0618 01560 (<unknown line number>)	NOP
	0x0618 01560 (/src/math/floor.go:100)	NOOP
	0x061c 01564 (/src/math/floor.go:101)	AND	$2047, R9, R9
	0x0620 01568 (/src/math/floor.go:102)	MOVV	$1023, R4
	0x0624 01572 (/src/math/floor.go:102)	BGEU	R9, R4, 1612
	0x0628 01576 (/src/math/floor.go:104)	MOVV	$-9223372036854775808, R10
	0x062c 01580 (/src/math/floor.go:104)	AND	R10, R8, R8
	0x0630 01584 (/src/math/floor.go:105)	XOR	$1022, R9, R9
	0x0634 01588 (/src/math/floor.go:105)	BNE	R9, 1604
	0x0638 01592 (/src/math/floor.go:106)	MOVV	$4607182418800017408, R9
	0x063c 01596 (/src/math/floor.go:106)	OR	R9, R8, R8
	0x0640 01600 (/src/math/floor.go:106)	JMP	1692
	0x0644 01604 (/src/math/floor.go:106)	MOVV	$4607182418800017408, R9
	0x0648 01608 (/src/math/floor.go:105)	JMP	1692
	0x064c 01612 (/src/math/floor.go:108)	MOVV	$1075, R5
	0x0650 01616 (/src/math/floor.go:108)	BGEU	R9, R5, 1664
	0x0654 01620 (/src/math/floor.go:114)	ADDVU	$-1023, R9, R9
	0x0658 01624 (/src/math/floor.go:115)	MOVV	$2251799813685248, R10
	0x0664 01636 (/src/math/floor.go:115)	SRLV	R9, R10, R11
	0x0668 01640 (/src/math/floor.go:115)	ADDVU	R11, R8, R11
	0x066c 01644 (/src/math/floor.go:116)	MOVV	$4503599627370495, R12
	0x0674 01652 (/src/math/floor.go:116)	SRLV	R9, R12, R9
	0x0678 01656 (/src/math/floor.go:116)	ANDN	R9, R11, R8
	0x067c 01660 (/src/math/floor.go:116)	JMP	1684
	0x0680 01664 (/src/math/floor.go:116)	MOVV	$2251799813685248, R10
	0x068c 01676 (/src/math/floor.go:116)	MOVV	$4503599627370495, R12
	0x0694 01684 (<unknown line number>)	MOVV	$4607182418800017408, R9
	0x0698 01688 (<unknown line number>)	MOVV	$-9223372036854775808, R10
	0x069c 01692 (<unknown line number>)	NOP
	0x069c 01692 (/src/math/floor.go:118)	NOOP
	0x06a0 01696 (/src/color/space.go:31)	CMPGED	F7, F0, FCC0
	0x06a4 01700 (/src/color/space.go:31)	MOVV	R0, R11
	0x06a8 01704 (/src/color/space.go:31)	BFPF	1712
	0x06ac 01708 (/src/color/space.go:31)	MOVV	$1, R11
	0x06b0 01712 (/src/color/space.go:31)	NOP
	0x06b0 01712 (/src/color/space.go:31)	MOVBU	R11, R11
	0x06b4 01716 (/src/color/space.go:31)	BEQ	R11, 1728
	0x06b8 01720 (/src/color/space.go:32)	MULD	F0, F2, F0
	0x06bc 01724 (/src/color/calibrated_test.go:354)	JMP	1792
	0x06c0 01728 (/src/color/calibrated_test.go:116)	MOVV	R8, math.bits-80(SP)
	0x06c4 01732 (<unknown line number>)	NOP
	0x06c4 01732 (<unknown line number>)	PCDATA	$0, $-4
	0x06c4 01732 (/src/math/pow.go:52)	MOVD	$f64.3fdaaaaaaaaaaaab(SB), F1
	0x06cc 01740 (/src/math/pow.go:52)	PCDATA	$0, $-1
	0x06cc 01740 (/src/math/pow.go:52)	CALL	math.pow(SB)
	0x06d0 01744 (/src/math/pow.go:52)	PCDATA	$0, $-3
	0x06d0 01744 (/src/color/space.go:34)	MOVD	$f64.3ff0e147ae147ae1(SB), F2
	0x06d8 01752 (/src/color/space.go:34)	PCDATA	$0, $-4
	0x06d8 01752 (/src/color/space.go:34)	MOVD	$f64.3fac28f5c28f5c29(SB), F3
	0x06e0 01760 (/src/color/space.go:34)	PCDATA	$0, $-1
	0x06e0 01760 (/src/color/space.go:34)	FMSUBD	F3, F2, F0, F0
	0x06e4 01764 (/src/math/floor.go:102)	MOVV	$1023, R4
	0x06e8 01768 (/src/math/unsafe.go:41)	MOVV	math.bits-80(SP), R8
	0x06ec 01772 (/src/math/unsafe.go:41)	MOVV	$4607182418800017408, R9
	0x06f0 01776 (/src/math/unsafe.go:41)	MOVV	$-9223372036854775808, R10
	0x06f4 01780 (/src/math/unsafe.go:41)	MOVV	R0, F4
	0x06f8 01784 (/src/math/unsafe.go:41)	PCDATA	$0, $-3
	0x06f8 01784 (/src/math/unsafe.go:41)	MOVD	$f64.406fe00000000000(SB), F5
	0x0700 01792 (/src/math/unsafe.go:41)	PCDATA	$0, $-1
	0x0700 01792 (<unknown line number>)	NOP
	0x0700 01792 (<unknown line number>)	NOP
	0x0700 01792 (/src/color/cmyk.go:111)	CMPGTD	F4, F0, FCC0
	0x0704 01796 (/src/color/cmyk.go:111)	MOVV	R0, R11
	0x0708 01800 (/src/color/cmyk.go:111)	BFPF	1808
	0x070c 01804 (/src/color/cmyk.go:111)	MOVV	$1, R11
	0x0710 01808 (/src/color/cmyk.go:111)	NOP
	0x0710 01808 (/src/color/cmyk.go:111)	MOVBU	R11, R11
	0x0714 01812 (/src/color/cmyk.go:111)	BEQ	R11, 1824
	0x0718 01816 (/src/color/cmyk.go:111)	MOVV	R0, F0
	0x071c 01820 (/src/color/calibrated_test.go:116)	JMP	1864
	0x0720 01824 (/src/color/calibrated_test.go:116)	PCDATA	$0, $-4
	0x0720 01824 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F1
	0x0728 01832 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0728 01832 (/src/color/cmyk.go:114)	CMPGTD	F0, F1, FCC0
	0x072c 01836 (/src/color/cmyk.go:114)	MOVV	R0, R11
	0x0730 01840 (/src/color/cmyk.go:114)	BFPF	1848
	0x0734 01844 (/src/color/cmyk.go:114)	MOVV	$1, R11
	0x0738 01848 (/src/color/cmyk.go:114)	NOP
	0x0738 01848 (/src/color/cmyk.go:114)	MOVBU	R11, R11
	0x073c 01852 (/src/color/cmyk.go:114)	BEQ	R11, 1864
	0x0740 01856 (/src/color/cmyk.go:114)	PCDATA	$0, $-3
	0x0740 01856 (/src/color/cmyk.go:114)	MOVD	$f64.3ff0000000000000(SB), F0
	0x0748 01864 (/src/color/cmyk.go:114)	PCDATA	$0, $-1
	0x0748 01864 (/src/color/calibrated_test.go:116)	MULD	F5, F0, F0
	0x074c 01868 (/src/math/unsafe.go:35)	MOVV	F0, R11
	0x0750 01872 (/src/math/floor.go:101)	SRLV	$52, R11, R12
	0x0754 01876 (<unknown line number>)	NOP
	0x0754 01876 (/src/math/floor.go:100)	NOOP
	0x0758 01880 (/src/math/floor.go:101)	AND	$2047, R12, R12
	0x075c 01884 (/src/math/floor.go:102)	BGEU	R12, R4, 1908
	0x0760 01888 (/src/math/floor.go:104)	AND	R10, R11, R10
	0x0764 01892 (/src/math/floor.go:105)	XOR	$1022, R12, R11
	0x0768 01896 (/src/math/floor.go:105)	BNE	R11, 1960
	0x076c 01900 (/src/math/floor.go:106)	OR	R9, R10, R10
	0x0770 01904 (/src/math/floor.go:106)	JMP	1960
	0x0774 01908 (/src/math/floor.go:108)	MOVV	$1075, R4
	0x0778 01912 (/src/math/floor.go:108)	BGEU	R12, R4, 1956
	0x077c 01916 (/src/math/floor.go:114)	ADDVU	$-1023, R12, R9
	0x0780 01920 (/src/math/floor.go:115)	MOVV	$2251799813685248, R10
	0x078c 01932 (/src/math/floor.go:115)	SRLV	R9, R10, R10
	0x0790 01936 (/src/math/floor.go:115)	ADDVU	R10, R11, R10
	0x0794 01940 (/src/math/floor.go:116)	MOVV	$4503599627370495, R12
	0x079c 01948 (/src/math/floor.go:116)	SRLV	R9, R12, R9
	0x07a0 01952 (/src/math/floor.go:116)	ANDN	R9, R10, R11
	0x07a4 01956 (/src/color/calibrated_test.go:116)	MOVV	R11, R10
	0x07a8 01960 (/src/math/unsafe.go:41)	MOVV	R8, F0
	0x07ac 01964 (/src/math/unsafe.go:41)	MOVV	R10, F1
	0x07b0 01968 (/src/math/floor.go:118)	NOOP
	0x07b4 01972 (/src/color/calibrated_test.go:116)	TRUNCDV	F0, F0
	0x07b8 01976 (/src/color/calibrated_test.go:116)	TRUNCDV	F1, F1
	0x07bc 01980 (/src/color/calibrated_test.go:356)	MOVV	F0, R4
	0x07c0 01984 (/src/color/calibrated_test.go:356)	MOVV	F1, R5
	0x07c4 01988 (/src/color/calibrated_test.go:356)	BNE	R4, R5, 2040
	0x07c8 01992 (/src/color/calibrated_test.go:357)	MOVV	$type:string(SB), R8
	0x07d0 02000 (/src/color/calibrated_test.go:357)	MOVV	R8, github.com/go-gfx/gfx/color..autotmp_121-64(SP)
	0x07d4 02004 (/src/color/calibrated_test.go:357)	MOVV	$github.com/go-gfx/gfx/color..stmp_20(SB), R8
	0x07dc 02012 (/src/color/calibrated_test.go:357)	MOVV	R8, github.com/go-gfx/gfx/color..autotmp_121-56(SP)
	0x07e0 02016 (/src/color/calibrated_test.go:357)	MOVV	github.com/go-gfx/gfx/color.t(FP), R4
	0x07e4 02020 (/src/color/calibrated_test.go:357)	PCDATA	$0, $-2
	0x07e4 02020 (/src/color/calibrated_test.go:357)	MOVB	(R4), R30
	0x07e8 02024 (/src/color/calibrated_test.go:357)	PCDATA	$0, $-1
	0x07e8 02024 (/src/color/calibrated_test.go:357)	MOVV	$github.com/go-gfx/gfx/color..autotmp_121-64(SP), R5
	0x07ec 02028 (/src/color/calibrated_test.go:357)	MOVV	$1, R6
	0x07f0 02032 (/src/color/calibrated_test.go:357)	MOVV	R6, R7
	0x07f4 02036 (/src/color/calibrated_test.go:357)	PCDATA	$1, $2
	0x07f4 02036 (/src/color/calibrated_test.go:357)	CALL	testing.(*common).Error(SB)
	0x07f8 02040 (/src/color/calibrated_test.go:359)	MOVV	(R3), R1
	0x07fc 02044 (/src/color/calibrated_test.go:359)	ADDV	$152, R3
	0x0800 02048 (/src/color/calibrated_test.go:359)	JMP	(R1)
	0x0804 02052 (/src/color/calibrated_test.go:359)	NOP
	0x0804 02052 (/src/color/calibrated_test.go:343)	PCDATA	$1, $-1
	0x0804 02052 (/src/color/calibrated_test.go:343)	PCDATA	$0, $-2
	0x0804 02052 (/src/color/calibrated_test.go:343)	MOVV	R4, 8(R3)
	0x0808 02056 (/src/color/calibrated_test.go:343)	MOVV	R1, R31
	0x080c 02060 (/src/color/calibrated_test.go:343)	CALL	runtime.morestack_noctxt(SB)
	0x0810 02064 (/src/color/calibrated_test.go:343)	PCDATA	$0, $-1
	0x0810 02064 (/src/color/calibrated_test.go:343)	MOVV	8(R3), R4
	0x0814 02068 (/src/color/calibrated_test.go:343)	JMP	0
	0x0000 d4 42 c0 28 78 a0 ff 02 94 e2 12 00 80 fa 07 40  .B.(x..........@
	0x0010 61 a0 fd 29 63 a0 fd 02 61 00 c0 29 64 80 c2 29  a..)c...a..)d..)
	0x0020 1e 00 00 1a c0 03 80 2b 01 a8 14 01 22 98 14 01  .......+...."...
	0x0030 1e 00 00 1a c3 03 80 2b 1e 00 00 1a c4 03 80 2b  .......+.......+
	0x0040 1e 00 00 1a c5 03 80 2b 00 00 00 54 06 a8 14 01  .......+...T....
	0x0050 00 98 21 0c 04 00 15 00 00 08 00 48 04 04 80 03  ..!........H....
	0x0060 84 fc 43 03 80 0c 00 40 03 a8 14 01 00 34 00 50  ..C....@.....4.P
	0x0070 1e 00 00 1a c4 03 80 2b 80 80 21 0c 05 00 15 00  .......+..!.....
	0x0080 00 08 00 48 05 04 80 03 a5 fc 43 03 a0 10 00 40  ...H......C....@
	0x0090 1e 00 00 1a c3 03 80 2b 00 08 00 50 03 98 14 01  .......+...P....
	0x00a0 1e 00 00 1a c7 03 80 2b e8 0c 05 01 05 b9 14 01  .......+........
	0x00b0 a6 d0 45 00 00 00 40 03 c6 fc 5f 03 07 fc 8f 03  ..E...@..._.....
	0x00c0 c7 28 00 6c 08 00 20 03 05 95 14 00 c6 f8 cf 03  .(.l.. .........
	0x00d0 c0 10 00 44 06 fc 0f 03 c5 14 15 00 00 5c 00 50  ...D.........\.P
	0x00e0 06 fc 0f 03 00 54 00 50 08 cc 90 03 c8 30 00 6c  .....T.P.....0.l
	0x00f0 c6 04 f0 02 08 00 80 02 08 00 00 17 08 01 00 03  ................
	0x0100 09 19 19 00 a9 a4 10 00 0a fc ff 02 4a 01 00 03  ............J...
	0x0110 46 19 19 00 25 99 16 00 00 18 00 50 08 00 80 02  F...%......P....
	0x0120 08 00 00 17 08 01 00 03 0a fc ff 02 4a 01 00 03  ............J...
	0x0130 06 fc 0f 03 08 00 20 03 00 00 40 03 40 98 21 0c  ...... ...@.@.!.
	0x0140 09 00 15 00 00 08 00 48 09 04 80 03 29 fd 43 03  .......H....).C.
	0x0150 20 0d 00 40 03 a8 14 01 00 34 00 50 1e 00 00 1a   ..@.....4.P....
	0x0160 c4 03 80 2b 80 88 21 0c 0a 00 15 00 00 08 00 48  ...+..!........H
	0x0170 0a 04 80 03 4a fd 43 03 40 11 00 40 1e 00 00 1a  ....J.C.@..@....
	0x0180 c3 03 80 2b 00 08 00 50 43 98 14 01 68 1c 05 01  ...+...PC...h...
	0x0190 0a b9 14 01 4b d1 45 00 00 00 40 03 6b fd 5f 03  ....K.E...@.k._.
	0x01a0 67 19 00 6c 4a a1 14 00 6b f9 cf 03 60 55 00 44  g..lJ...k...`U.D
	0x01b0 4a 19 15 00 00 4c 00 50 0c cc 90 03 6c 31 00 6c  J....L.P....l1.l
	0x01c0 6b 05 f0 02 0c 00 80 02 0c 00 00 17 8c 01 00 03  k...............
	0x01d0 8d 2d 19 00 4d b5 10 00 0e fc ff 02 ce 01 00 03  .-..M...........
	0x01e0 cb 2d 19 00 aa ad 16 00 00 18 00 50 0c 00 80 02  .-.........P....
	0x01f0 0c 00 00 17 8c 01 00 03 0e fc ff 02 ce 01 00 03  ................
	0x0200 a8 a8 14 01 08 a9 1a 01 49 a9 14 01 29 a9 1a 01  ........I...)...
	0x0210 05 b9 14 01 2a b9 14 01 a5 a8 11 00 00 00 40 03  ....*.........@.
	0x0220 a5 04 c0 02 0a 08 80 03 45 b1 02 6c 80 0c 00 40  ........E..l...@
	0x0230 00 a8 14 01 00 2c 00 50 1e 00 00 1a c3 03 80 2b  .....,.P.......+
	0x0240 60 80 21 0c 05 00 15 00 00 08 00 48 05 04 80 03  `.!........H....
	0x0250 a5 fc 43 03 a0 0c 00 40 1e 00 00 1a c0 03 80 2b  ..C....@.......+
	0x0260 00 1c 05 01 05 b8 14 01 aa d0 45 00 00 00 40 03  ..........E...@.
	0x0270 4a fd 5f 03 47 19 00 6c a5 a0 14 00 4a f9 cf 03  J._.G..l....J...
	0x0280 40 55 00 44 a5 18 15 00 00 4c 00 50 04 cc 90 03  @U.D.....L.P....
	0x0290 44 31 00 6c 4a 05 f0 02 0b 00 80 02 0b 00 00 17  D1.lJ...........
	0x02a0 6b 01 00 03 6c 29 19 00 ac b0 10 00 0d fc ff 02  k...l)..........
	0x02b0 ad 01 00 03 aa 29 19 00 85 a9 16 00 00 18 00 50  .....).........P
	0x02c0 0b 00 80 02 0b 00 00 17 6b 01 00 03 0d fc ff 02  ........k.......
	0x02d0 ad 01 00 03 00 00 40 03 20 98 21 0c 0a 00 15 00  ......@. .!.....
	0x02e0 00 08 00 48 0a 04 80 03 4a fd 43 03 40 0d 00 40  ...H....J.C.@..@
	0x02f0 01 a8 14 01 00 2c 00 50 1e 00 00 1a c0 03 80 2b  .....,.P.......+
	0x0300 00 84 21 0c 0a 00 15 00 00 08 00 48 0a 04 80 03  ..!........H....
	0x0310 4a fd 43 03 40 0d 00 40 1e 00 00 1a c1 03 80 2b  J.C.@..@.......+
	0x0320 20 1c 05 01 0a b8 14 01 4b d1 45 00 00 00 40 03   .......K.E...@.
	0x0330 6b fd 5f 03 67 19 00 6c 4a a1 14 00 6b f9 cf 03  k._.g..lJ...k...
	0x0340 60 55 00 44 4a 19 15 00 00 4c 00 50 04 cc 90 03  `U.DJ....L.P....
	0x0350 64 31 00 6c 6b 05 f0 02 0c 00 80 02 0c 00 00 17  d1.lk...........
	0x0360 8c 01 00 03 8d 2d 19 00 4d b5 10 00 0e fc ff 02  .....-..M.......
	0x0370 ce 01 00 03 cb 2d 19 00 aa ad 16 00 00 18 00 50  .....-.........P
	0x0380 0c 00 80 02 0c 00 00 17 8c 01 00 03 0e fc ff 02  ................
	0x0390 ce 01 00 03 00 00 40 03 20 0d 00 40 02 a8 14 01  ......@. ..@....
	0x03a0 00 2c 00 50 1e 00 00 1a c0 03 80 2b 00 88 21 0c  .,.P.......+..!.
	0x03b0 09 00 15 00 00 08 00 48 09 04 80 03 29 fd 43 03  .......H....).C.
	0x03c0 20 0d 00 40 1e 00 00 1a c2 03 80 2b 40 1c 05 01   ..@.......+@...
	0x03d0 09 b8 14 01 2b d1 45 00 00 00 40 03 6b fd 5f 03  ....+.E...@.k._.
	0x03e0 67 19 00 6c 27 a1 14 00 68 f9 cf 03 00 41 00 44  g..l'...h....A.D
	0x03f0 e7 18 15 00 00 38 00 50 04 cc 90 03 64 2d 00 6c  .....8.P....d-.l
	0x0400 66 05 f0 02 07 00 80 02 07 00 00 17 e7 00 00 03  f...............
	0x0410 e7 18 19 00 27 9d 10 00 08 fc ff 02 08 01 00 03  ....'...........
	0x0420 06 19 19 00 e9 98 16 00 27 01 15 00 a0 a8 14 01  ........'.......
	0x0430 00 a8 1a 01 41 a9 14 01 21 a8 1a 01 61 00 c1 2b  ....A...!...a..+
	0x0440 e1 a8 14 01 21 a8 1a 01 61 e0 c0 2b 00 00 40 03  ....!...a..+..@.
	0x0450 65 a0 c1 02 a0 00 c0 29 a0 20 c0 29 a0 40 c0 29  e......). .).@.)
	0x0460 a0 60 c0 29 a0 80 c0 29 a0 a0 c0 29 04 b8 14 01  .`.)...)...)....
	0x0470 00 00 00 54 05 00 00 1a a5 00 c0 02 65 a0 c1 29  ...T........e..)
	0x0480 64 c0 c1 29 64 00 c1 28 00 00 00 54 05 00 00 1a  d..)d..(...T....
	0x0490 a5 00 c0 02 65 e0 c1 29 64 00 c2 29 64 e0 c0 28  ....e..)d..)d..(
	0x04a0 00 00 00 54 05 00 00 1a a5 00 c0 02 65 20 c2 29  ...T........e .)
	0x04b0 64 40 c2 29 64 80 c2 28 9e 00 00 28 05 00 00 1a  d@.)d..(...(....
	0x04c0 a5 00 c0 02 06 dc 80 03 67 a0 c1 02 08 0c 80 03  ........g.......
	0x04d0 09 01 15 00 00 00 00 54 1e 00 00 1a c0 03 80 2b  .......T.......+
	0x04e0 01 a8 14 01 22 98 14 01 1e 00 00 1a c3 03 80 2b  ...."..........+
	0x04f0 1e 00 00 1a c4 03 80 2b 1e 00 00 1a c5 03 80 2b  .......+.......+
	0x0500 00 00 00 54 1e 00 00 1a c6 03 80 2b c6 04 05 01  ...T.......+....
	0x0510 1e 00 00 1a c7 03 80 2b 06 1c 63 08 1e 00 00 1a  .......+..c.....
	0x0520 c7 03 80 2b e6 08 e3 08 1e 00 00 1a c7 03 80 2b  ...+...........+
	0x0530 e7 04 05 01 1e 00 00 1a c8 03 80 2b 07 a0 63 08  ...........+..c.
	0x0540 1e 00 00 1a c8 03 80 2b 00 89 23 08 1e 00 00 1a  .......+..#.....
	0x0550 c7 03 80 2b c0 9c 23 0c 04 00 15 00 00 08 00 48  ...+..#........H
	0x0560 04 04 80 03 84 fc 43 03 80 14 00 40 1e 00 00 1a  ......C....@....
	0x0570 c2 03 80 2b 43 18 05 01 00 40 00 50 60 40 c1 2b  ...+C....@.P`@.+
	0x0580 c0 98 14 01 1e 00 00 1a c1 03 80 2b 00 00 00 54  ...........+...T
	0x0590 1e 00 00 1a c2 03 80 2b 1e 00 00 1a c3 03 80 2b  .......+.......+
	0x05a0 43 80 61 08 60 40 81 2b 1e 00 00 1a c2 03 80 2b  C.a.`@.+.......+
	0x05b0 1e 00 00 1a c7 03 80 2b 04 a8 14 01 60 90 21 0c  .......+....`.!.
	0x05c0 08 00 15 00 00 08 00 48 08 04 80 03 08 fd 43 03  .......H......C.
	0x05d0 00 0d 00 40 03 a8 14 01 00 2c 00 50 1e 00 00 1a  ...@.....,.P....
	0x05e0 c5 03 80 2b a0 8c 21 0c 08 00 15 00 00 08 00 48  ...+..!........H
	0x05f0 08 04 80 03 08 fd 43 03 00 0d 00 40 1e 00 00 1a  ......C....@....
	0x0600 c3 03 80 2b 1e 00 00 1a c5 03 80 2b 63 14 05 01  ...+.......+c...
	0x0610 68 b8 14 01 09 d1 45 00 00 00 40 03 29 fd 5f 03  h.....E...@.)._.
	0x0620 04 fc 8f 03 24 29 00 6c 0a 00 20 03 08 a9 14 00  ....$).l.. .....
	0x0630 29 f9 cf 03 20 11 00 44 09 fc 0f 03 08 25 15 00  )... ..D.....%..
	0x0640 00 5c 00 50 09 fc 0f 03 00 54 00 50 05 cc 90 03  .\.P.....T.P....
	0x0650 25 31 00 6c 29 05 f0 02 0a 00 80 02 0a 00 00 17  %1.l)...........
	0x0660 4a 01 00 03 4b 25 19 00 0b ad 10 00 0c fc ff 02  J...K%..........
	0x0670 8c 01 00 03 89 25 19 00 68 a5 16 00 00 18 00 50  .....%..h......P
	0x0680 0a 00 80 02 0a 00 00 17 4a 01 00 03 0c fc ff 02  ........J.......
	0x0690 8c 01 00 03 09 fc 0f 03 0a 00 20 03 00 00 40 03  .......... ...@.
	0x06a0 00 9c 23 0c 0b 00 15 00 00 08 00 48 0b 04 80 03  ..#........H....
	0x06b0 6b fd 43 03 60 0d 00 40 40 00 05 01 00 44 00 50  k.C.`..@@....D.P
	0x06c0 68 20 c1 29 1e 00 00 1a c1 03 80 2b 00 00 00 54  h .).......+...T
	0x06d0 1e 00 00 1a c2 03 80 2b 1e 00 00 1a c3 03 80 2b  .......+.......+
	0x06e0 00 88 61 08 04 fc 8f 03 68 20 c1 28 09 fc 0f 03  ..a.....h .(....
	0x06f0 0a 00 20 03 04 a8 14 01 1e 00 00 1a c5 03 80 2b  .. ............+
	0x0700 00 90 21 0c 0b 00 15 00 00 08 00 48 0b 04 80 03  ..!........H....
	0x0710 6b fd 43 03 60 0d 00 40 00 a8 14 01 00 2c 00 50  k.C.`..@.....,.P
	0x0720 1e 00 00 1a c1 03 80 2b 20 80 21 0c 0b 00 15 00  .......+ .!.....
	0x0730 00 08 00 48 0b 04 80 03 6b fd 43 03 60 0d 00 40  ...H....k.C.`..@
	0x0740 1e 00 00 1a c0 03 80 2b 00 14 05 01 0b b8 14 01  .......+........
	0x0750 6c d1 45 00 00 00 40 03 8c fd 5f 03 84 19 00 6c  l.E...@..._....l
	0x0760 6a a9 14 00 8b f9 cf 03 60 41 00 44 4a 25 15 00  j.......`A.DJ%..
	0x0770 00 38 00 50 04 cc 90 03 84 2d 00 6c 89 05 f0 02  .8.P.....-.l....
	0x0780 0a 00 80 02 0a 00 00 17 4a 01 00 03 4a 25 19 00  ........J...J%..
	0x0790 6a a9 10 00 0c fc ff 02 8c 01 00 03 89 25 19 00  j............%..
	0x07a0 4b a5 16 00 6a 01 15 00 00 a9 14 01 41 a9 14 01  K...j.......A...
	0x07b0 00 00 40 03 00 a8 1a 01 21 a8 1a 01 04 b8 14 01  ..@.....!.......
	0x07c0 25 b8 14 01 85 34 00 5c 08 00 00 1a 08 01 c0 02  %....4.\........
	0x07d0 68 60 c1 29 08 00 00 1a 08 01 c0 02 68 80 c1 29  h`.)........h..)
	0x07e0 64 80 c2 28 9e 00 00 28 65 60 c1 02 06 04 80 03  d..(...(e`......
	0x07f0 c7 00 15 00 00 00 00 54 61 00 c0 28 63 60 c2 02  .......Ta..(c`..
	0x0800 20 00 00 4c 64 20 c0 29 3f 00 15 00 00 00 00 54   ..Ld .)?......T
	0x0810 64 20 c0 28 ff ef f7 53                          d .(...S
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:int+0
	rel 0+0 t=R_USEIFACE type:string+0
	rel 32+4 t=R_LOONG64_ADDR_HI $f64.4049000000000000+0
	rel 36+4 t=R_LOONG64_ADDR_LO $f64.4049000000000000+0
	rel 48+4 t=R_LOONG64_ADDR_HI $f64.3feedb8bac710cb3+0
	rel 52+4 t=R_LOONG64_ADDR_LO $f64.3feedb8bac710cb3+0
	rel 56+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 60+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 64+4 t=R_LOONG64_ADDR_HI $f64.3fea67381d7dbf48+0
	rel 68+4 t=R_LOONG64_ADDR_LO $f64.3fea67381d7dbf48+0
	rel 72+4 t=R_CALLLOONG64 github.com/go-gfx/gfx/color.LabToSRGBWP+0
	rel 112+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 116+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 144+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 148+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 160+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 164+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 348+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 352+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 380+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 384+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 568+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 572+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 600+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 604+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 760+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 764+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 792+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 796+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 932+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 936+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 964+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 968+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 1136+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 1140+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 1144+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 1160+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 1164+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 1168+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 1184+4 t=R_CALLLOONG64 runtime.convT64+0
	rel 1188+4 t=R_LOONG64_ADDR_HI type:int+0
	rel 1192+4 t=R_LOONG64_ADDR_LO type:int+0
	rel 1212+4 t=R_LOONG64_ADDR_HI go:string."a neutral Lab under D50 came back with a cast: %d %d %d"+0
	rel 1216+4 t=R_LOONG64_ADDR_LO go:string."a neutral Lab under D50 came back with a cast: %d %d %d"+0
	rel 1236+4 t=R_CALLLOONG64 testing.(*common).Errorf+0
	rel 1240+4 t=R_LOONG64_ADDR_HI $f64.4049000000000000+0
	rel 1244+4 t=R_LOONG64_ADDR_LO $f64.4049000000000000+0
	rel 1256+4 t=R_LOONG64_ADDR_HI $f64.3feedb8bac710cb3+0
	rel 1260+4 t=R_LOONG64_ADDR_LO $f64.3feedb8bac710cb3+0
	rel 1264+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 1268+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 1272+4 t=R_LOONG64_ADDR_HI $f64.3fea67381d7dbf48+0
	rel 1276+4 t=R_LOONG64_ADDR_LO $f64.3fea67381d7dbf48+0
	rel 1280+4 t=R_CALLLOONG64 github.com/go-gfx/gfx/color.LabToXYZWP+0
	rel 1284+4 t=R_LOONG64_ADDR_HI $f64.3ff8981e8a2ec28b+0
	rel 1288+4 t=R_LOONG64_ADDR_LO $f64.3ff8981e8a2ec28b+0
	rel 1296+4 t=R_LOONG64_ADDR_HI $f64.4009ec7340697c9b+0
	rel 1300+4 t=R_LOONG64_ADDR_LO $f64.4009ec7340697c9b+0
	rel 1308+4 t=R_LOONG64_ADDR_HI $f64.3fdfe7f03ec1dcaf+0
	rel 1312+4 t=R_LOONG64_ADDR_LO $f64.3fdfe7f03ec1dcaf+0
	rel 1320+4 t=R_LOONG64_ADDR_HI $f64.3fca1d854c04bb51+0
	rel 1324+4 t=R_LOONG64_ADDR_LO $f64.3fca1d854c04bb51+0
	rel 1332+4 t=R_LOONG64_ADDR_HI $f64.3fac7d4aae79fb6f+0
	rel 1336+4 t=R_LOONG64_ADDR_LO $f64.3fac7d4aae79fb6f+0
	rel 1344+4 t=R_LOONG64_ADDR_HI $f64.3ff0ea64f8a81cea+0
	rel 1348+4 t=R_LOONG64_ADDR_LO $f64.3ff0ea64f8a81cea+0
	rel 1356+4 t=R_LOONG64_ADDR_HI $f64.3f69a5c37387b719+0
	rel 1360+4 t=R_LOONG64_ADDR_LO $f64.3f69a5c37387b719+0
	rel 1388+4 t=R_LOONG64_ADDR_HI $f64.4029d70a3d70a3d7+0
	rel 1392+4 t=R_LOONG64_ADDR_LO $f64.4029d70a3d70a3d7+0
	rel 1412+4 t=R_LOONG64_ADDR_HI $f64.3fdaaaaaaaaaaaab+0
	rel 1416+4 t=R_LOONG64_ADDR_LO $f64.3fdaaaaaaaaaaaab+0
	rel 1420+4 t=R_CALLLOONG64 math.pow+0
	rel 1424+4 t=R_LOONG64_ADDR_HI $f64.3ff0e147ae147ae1+0
	rel 1428+4 t=R_LOONG64_ADDR_LO $f64.3ff0e147ae147ae1+0
	rel 1432+4 t=R_LOONG64_ADDR_HI $f64.3fac28f5c28f5c29+0
	rel 1436+4 t=R_LOONG64_ADDR_LO $f64.3fac28f5c28f5c29+0
	rel 1448+4 t=R_LOONG64_ADDR_HI $f64.4029d70a3d70a3d7+0
	rel 1452+4 t=R_LOONG64_ADDR_LO $f64.4029d70a3d70a3d7+0
	rel 1456+4 t=R_LOONG64_ADDR_HI $f64.3f69a5c37387b719+0
	rel 1460+4 t=R_LOONG64_ADDR_LO $f64.3f69a5c37387b719+0
	rel 1500+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 1504+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 1532+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 1536+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 1540+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 1544+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 1732+4 t=R_LOONG64_ADDR_HI $f64.3fdaaaaaaaaaaaab+0
	rel 1736+4 t=R_LOONG64_ADDR_LO $f64.3fdaaaaaaaaaaaab+0
	rel 1740+4 t=R_CALLLOONG64 math.pow+0
	rel 1744+4 t=R_LOONG64_ADDR_HI $f64.3ff0e147ae147ae1+0
	rel 1748+4 t=R_LOONG64_ADDR_LO $f64.3ff0e147ae147ae1+0
	rel 1752+4 t=R_LOONG64_ADDR_HI $f64.3fac28f5c28f5c29+0
	rel 1756+4 t=R_LOONG64_ADDR_LO $f64.3fac28f5c28f5c29+0
	rel 1784+4 t=R_LOONG64_ADDR_HI $f64.406fe00000000000+0
	rel 1788+4 t=R_LOONG64_ADDR_LO $f64.406fe00000000000+0
	rel 1824+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 1828+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 1856+4 t=R_LOONG64_ADDR_HI $f64.3ff0000000000000+0
	rel 1860+4 t=R_LOONG64_ADDR_LO $f64.3ff0000000000000+0
	rel 1992+4 t=R_LOONG64_ADDR_HI type:string+0
	rel 1996+4 t=R_LOONG64_ADDR_LO type:string+0
	rel 2004+4 t=R_LOONG64_ADDR_HI github.com/go-gfx/gfx/color..stmp_20+0
	rel 2008+4 t=R_LOONG64_ADDR_LO github.com/go-gfx/gfx/color..stmp_20+0
	rel 2036+4 t=R_CALLLOONG64 testing.(*common).Error+0
	rel 2060+4 t=R_CALLLOONG64 runtime.morestack_noctxt+0
